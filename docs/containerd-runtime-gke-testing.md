# Containerd Runtime — Implementation & GKE Testing Guide

## Background

The containerd runtime mode eliminates Docker-in-Docker (DinD) from launcher pods. Instead of
starting a dockerd inside the pod and doing `nerdctl save | docker load` for each image (which
costs 30–60s for 2–3 GB NOS images), the launcher talks directly to the **node's containerd
daemon** via its socket.

This only works when the node's CRI is **containerd** — which is the default on GKE, AKS, and EKS.
On cri-o clusters (like this on-prem cluster) it cannot work.

---

## What Was Implemented

### 1. Containerlab Fork — `runtime/containerd` restored

**Repo**: `/root/containerlab/containerlab` (fork of `github.com/srl-labs/containerlab`)

The `containerd` runtime was removed upstream in commit `fa610ec47`. It has been restored with
these changes:

#### Files changed

| File | Change |
|------|--------|
| `runtime/containerd/containerd.go` | **New** — restored + modified (see below) |
| `runtime/all/all.go` | Added `_ "github.com/srl-labs/containerlab/runtime/containerd"` import |
| `utils/containers.go` | Restored `GetCNIBinaryPath()` |
| `utils/netlink.go` | Restored `CheckBrInUse()`, `DeleteLinkByName()` |
| `go.mod` | Added `github.com/containerd/containerd v1.7.23`, `github.com/containernetworking/cni v1.3.0` |

#### Key modifications in `runtime/containerd/containerd.go`

- **Named netns instead of `/proc/<pid>/ns/net`**: `GetNSPath()` returns `/run/netns/<name>`.
  No `hostPID` needed. The named netns is created before `task.Start()` via
  `netns.NewNamed(nodecfg.LongName)`.
- **No `utils.LinkContainerNS()` call**: the named netns IS the path, not a symlink.
- **`DeleteContainer()`**: calls `netns.DeleteNamed(containerID)` after CNI cleanup.
- **MTU is `int`** (was `string` in old API): `WithMgmtNet()` no longer sets a string default;
  `cniInit()` uses `%d` format.
- **Import path**: `"github.com/srl-labs/containerlab/clab/exec"` → `"github.com/srl-labs/containerlab/exec"`
- **Logger**: `log "github.com/sirupsen/logrus"` → `"github.com/charmbracelet/log"`
- **New interface methods** (added since original): `IsHealthy`, `WriteToStdinNoWait`,
  `CheckConnection`, `GetRuntimeSocket`, `GetCooCBindMounts`, `StreamLogs`, `StreamEvents`,
  `InspectImage`, `CopyToContainer`

#### `GetCooCBindMounts()` — what the launcher pod gets

```go
types.Binds{
    types.NewBind("/run/netns", "/run/netns", "rshared"),
    types.NewBind("/run/containerd/containerd.sock", "/run/containerd/containerd.sock", ""),
}
```

---

### 2. Clabernetes — `controllers/topology/deployment.go`

When `containerRuntime: containerd` is set on a topology:

- **Volume added**: `/run/netns` as `HostPathDirectoryOrCreate`
- **VolumeMount added**: `/run/netns` with `MountPropagationBidirectional`

This bidirectional mount means named netns created inside the launcher pod (by the containerd
runtime calling `netns.NewNamed()`) propagate to the host node. The host containerd daemon can
then find the netns and attach the container's network namespace to it.

---

### 3. Launcher Images

Built with `RUNTIME_MODE` build arg in `build/launcher.Dockerfile`:

| Tag | Mode | Containerlab binary |
|-----|------|---------------------|
| `fix9` | `docker` (DinD, default) | apt-installed clab 0.73.0+ |
| `fix9c` | `containerd` (no dockerd, has CNI plugins) | fork binary from `build/containerlab_fork` |

The fork binary is built from `/root/containerlab/containerlab` and placed at
`build/containerlab_fork` before the docker build. The Dockerfile copies it and replaces the
apt-installed `/usr/bin/containerlab` when `RUNTIME_MODE=containerd`.

#### Build commands

```bash
# Build fork binary
cd /root/containerlab/containerlab
go build -o /root/containerlab/clabernetes/build/containerlab_fork .

# Build launcher (docker mode)
cd /root/containerlab/clabernetes
docker build --build-arg RUNTIME_MODE=docker \
  -t harbor.local/clabernetes/clabernetes-launcher:fix9 \
  -f ./build/launcher.Dockerfile .

# Build launcher (containerd mode)
docker build --build-arg RUNTIME_MODE=containerd \
  -t harbor.local/clabernetes/clabernetes-launcher:fix9c \
  -f ./build/launcher.Dockerfile .
```

---

### 4. CRD Update

The `containerRuntime` field was added to `TopologySpec.Deployment`. The CRD was regenerated:

```bash
controller-gen crd paths="./apis/..." output:crd:artifacts:config=/tmp/generated-crds
kubectl apply -f /tmp/generated-crds/
cp /tmp/generated-crds/*.yaml charts/clabernetes/crds/
```

The updated CRDs are committed in `charts/clabernetes/crds/`.

---

## GKE Testing Checklist

### Prerequisites

1. **GKE cluster** with nodes running containerd (default for GKE — verify with
   `kubectl get nodes -o wide` and check `CONTAINER-RUNTIME` column shows `containerd://...`)

2. **Registry accessible from GKE pods** — push images to a registry reachable from GKE
   (e.g. GCR, Artifact Registry, or a public registry). The local `harbor.local` is not
   reachable from GKE.

3. **Push images to GKE-accessible registry**:
   ```bash
   # Example with Google Artifact Registry
   AR_REPO=us-central1-docker.pkg.dev/YOUR_PROJECT/clabernetes

   docker tag harbor.local/clabernetes/clabernetes-manager:fix9 $AR_REPO/clabernetes-manager:fix9
   docker tag harbor.local/clabernetes/clabernetes-launcher:fix9c $AR_REPO/clabernetes-launcher:fix9c
   docker push $AR_REPO/clabernetes-manager:fix9
   docker push $AR_REPO/clabernetes-launcher:fix9c
   ```

4. **Install clabernetes** via helm with updated values pointing to your registry.

5. **Apply updated CRDs** (they include the `containerRuntime` field):
   ```bash
   kubectl apply -f charts/clabernetes/crds/
   ```

### Helm Values for GKE

```yaml
appName: clabernetes

manager:
  image: "YOUR_REGISTRY/clabernetes-manager:fix9"
  imagePullPolicy: Always

globalConfig:
  enabled: true
  mergeMode: merge

  imagePull:
    imagePullThroughMode: never   # GKE nodes already have images; or use "auto"

  deployment:
    launcherImage: "YOUR_REGISTRY/clabernetes-launcher:fix9"  # docker mode (default)
    launcherImagePullPolicy: Always
    privilegedLauncher: true

  naming: prefixed
```

### SRL Test Topology (containerd runtime)

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: srl-containerd-test
  namespace: clab
spec:
  connectivity: vxlan
  definition:
    containerlab: |
      name: srl-containerd-test
      topology:
        nodes:
          srl1:
            kind: srl
            image: ghcr.io/nokia/srlinux:latest
          srl2:
            kind: srl
            image: ghcr.io/nokia/srlinux:latest
        links:
          - endpoints: ["srl1:e1-1", "srl2:e1-1"]
  deployment:
    containerRuntime: containerd
    launcherImage: "YOUR_REGISTRY/clabernetes-launcher:fix9c"
    privilegedLauncher: true
```

Apply with: `kubectl apply -n clab -f srl-containerd-test.yaml`

### What to Verify

#### 1. Pod starts without dockerd
```bash
kubectl exec -n clab <srl1-pod> -- ps aux | grep -E "dockerd|containerd"
# Should see NO dockerd process
# Should see containerlab process using containerd runtime
```

#### 2. No `nerdctl save | docker load` in logs
```bash
kubectl logs -n clab <srl1-pod> | grep -E "save|load|pull"
# Should NOT see image export/import steps
```

#### 3. Named netns propagates to host
```bash
# On the GKE node running the pod:
ls /run/netns/
# Should see clab-<topology>-<node> entries
```

#### 4. NOS container visible via nerdctl on host
```bash
# On the GKE node:
nerdctl --namespace clab ps
# Should show SRL container
```

#### 5. SRL boots and is reachable
```bash
# Get launcher SSH key
kubectl exec -n clab <srl1-pod> -- cat /root/.ssh/id_rsa > /tmp/srl_key && chmod 600 /tmp/srl_key

# Get LoadBalancer IP
kubectl get topology srl-containerd-test -n clab -o jsonpath='{.status.exposedPorts.srl1.loadBalancerAddress}'

# SSH to SRL
ssh -o StrictHostKeyChecking=no -i /tmp/srl_key admin@<LB_IP> -p 22 "show version"
```

#### 6. VXLAN inter-node ping
```bash
# On SRL1 via SSH:
# Configure IP on ethernet-1/1
enter candidate
set / network-instance default type ip-vrf
set / network-instance default interface ethernet-1/1.0
set / interface ethernet-1/1 subinterface 0 ipv4 admin-state enable address 10.1.1.1/30
commit now

# On SRL2 via SSH (10.1.1.2/30)
# Then ping from SRL1:
ping 10.1.1.2 network-instance default -c 5
```

Expected: 0% packet loss, RTT <5ms.

#### 7. Startup time comparison
Compare startup time between:
- `launcherImage: fix9` (docker/DinD mode) — baseline
- `launcherImage: fix9c` with `containerRuntime: containerd` — should be 30–60s faster for large images

---

## Architecture: How the Containerd Mode Works on GKE

```
GKE node (containerd CRI)
├── containerd daemon at /run/containerd/containerd.sock
├── kubelet uses containerd to create launcher pod
└── launcher pod
    ├── /run/netns mounted Bidirectional (propagates to host)
    ├── /run/containerd/containerd.sock mounted RW
    ├── NO dockerd running
    └── containerlab --runtime containerd --containerd-socket /clabernetes/.node/containerd.sock
        └── containerd creates SRL container in namespace "clab"
            └── network namespace: /run/netns/clab-srl-containerd-test-srl1
                (visible on host via Bidirectional mount)
```

The named netns at `/run/netns/<name>` is created by `netns.NewNamed()` in the containerd
runtime BEFORE the containerd task starts. containerd then uses this existing netns for the
container's network namespace. The Bidirectional mount on `/run/netns` means this file appears
on the host node, where containerd can find it.

---

## Known Limitations / Watch Out For

1. **`privilegedLauncher: true` required** — the Bidirectional mount propagation and netns
   creation require privileged pod security context.

2. **GKE Autopilot**: may block privileged pods. Use GKE Standard instead.

3. **CNI plugins in launcher image** — the `fix9c` image includes CNI plugins at `/opt/cni/bin`
   (tuning, bridge, host-local, portmap). Required for the containerd runtime's `cniInit()` to
   assign management IPs to NOS containers.

4. **containerd socket path on GKE**: typically `/run/containerd/containerd.sock`. The launcher
   socket path constant `KubernetesCRISockContainerd = "containerd.sock"` with path
   `KubernetesCRISockContainerdPath = "/run/containerd"` should match. If GKE uses a different
   path, override with `imagePullCriSockOverride` in the global config.

5. **Image pull policy**: with `containerRuntime: containerd`, images go directly into the node's
   containerd store (namespace `clab`). If the same node is reused, the image will already be
   present on subsequent runs — much faster startup.

6. **SR-SIM on GKE**: SR-SIM requires significant memory (4g+) and specific node labels
   (`srsim-capable: "true"`). Ensure node pool has sufficient resources and the label is applied.
   The SR-SIM image also needs to be accessible from GKE nodes.

---

## Current Image Tags (harbor.local)

| Image | Tag | Mode |
|-------|-----|------|
| `clabernetes-manager` | `fix9` | — |
| `clabernetes-launcher` | `fix9` | docker/DinD (default) |
| `clabernetes-launcher` | `fix9c` | containerd (fork binary, CNI plugins) |

---

## On-Prem Test Results (fix9, docker mode)

Cluster: k8s-controller-new + k8s-worker1/2/3, all workers use **cri-o** (containerd mode N/A)

| Topology | Nodes | Status | SSH | Ping |
|----------|-------|--------|-----|------|
| `clab/srl-lab` | srl1 (worker2), srl2 (worker2) | ✅ Running, Ready | ✅ SR Linux v25.10.2 | ✅ 0% loss, ~2ms RTT |
| `safa/small-srsim` | R1 (worker1), R2 (worker3) | ✅ Running, Ready | ✅ TiMOS-C-25.7.R1 | VXLAN up, tc mirror set |
