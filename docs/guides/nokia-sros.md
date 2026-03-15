# Nokia SR OS (vr-sros) Support Guide

This guide explains how to deploy Nokia SR OS (`nokia_sros`) topologies with Clabernetes, including single-card and multi-linecard configurations using [srl-labs/vrnetlab](https://github.com/srl-labs/vrnetlab) images.

## Overview

`nokia_sros` is the vrnetlab-based kind for Nokia SR OS. It runs a full SR OS VM inside a container using QEMU. The container image is built from a `.qcow2` disk image using the srl-labs/vrnetlab build system.

**Key difference from `nokia_srsim`:** SR-SIM (`nokia_srsim`) is a native containerized simulator, while `nokia_sros` runs the actual SR OS VM image via vrnetlab/QEMU.

## Prerequisites

1. **Image**: Build the vrnetlab image from [srl-labs/vrnetlab](https://github.com/srl-labs/vrnetlab) using a Nokia SR OS `.qcow2` file. Push the resulting image to your container registry.

2. **License**: A valid SR OS license file. Without a license, the router boots in a degraded mode and shuts down after ~30 minutes (useful for testing).

3. **Resources**: SR OS nodes run QEMU VMs and require more CPU/memory than native container workloads. Multi-linecard types need significantly more resources.

## Single-Card (Integrated) Systems

Single-card types run one QEMU VM per container:

| Platform Type | Description |
|---------------|-------------|
| `sr-1` | SR-1 integrated system |
| `sr-1s` | SR-1s integrated system |
| *(empty)* | Default single-card behavior |

**Example:**

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: sros-single
spec:
  deployment:
    resources:
      sros1:
        requests:
          memory: "4Gi"
          cpu: "2"
        limits:
          memory: "8Gi"
          cpu: "4"
  definition:
    containerlab: |
      name: sros-single
      topology:
        kinds:
          nokia_sros:
            image: vrnetlab/nokia_sros:24.7.R1
            license: /opt/nokia/sros/license.txt
        nodes:
          sros1:
            kind: nokia_sros
            type: sr-1
        links: []
```

## Multi-Linecard Systems

Multi-linecard types (SR-7, SR-14s, etc.) run **all VMs inside a single container**. The vrnetlab entrypoint spawns multiple QEMU instances:

- **CP VM** on QEMU monitor port 4000
- **LC VM(s)** on QEMU monitor ports 4001, 4002, ...

This is fundamentally different from `nokia_srsim` distributed mode, which uses separate containers per card joined via `network-mode: container:<primary>`.

### The `cp:/lc:` Type Format

Multi-linecard deployments use a special type string format:

```
cp: cpu=<N> ram=<N> chassis=<type> slot=<slot> card=<card> ___lc: cpu=<N> ram=<N> max_nics=<N> chassis=<type> slot=<slot> card=<card> mda/<N>=<mda-type>
```

The `___` separator (three underscores) delimits the CP and LC sections. vrnetlab parses this string to determine how many QEMU VMs to spawn and their parameters.

**Parameters:**

| Section | Parameter | Description |
|---------|-----------|-------------|
| `cp:` | `cpu` | vCPUs for the CP VM |
| `cp:` | `ram` | RAM (GB) for the CP VM |
| `cp:` | `chassis` | Chassis type (sr-7, sr-14s) |
| `cp:` | `slot` | Card slot (A for CPM-A) |
| `cp:` | `card` | Card type (cpm5, cpm6, etc.) |
| `lc:` | `cpu` | vCPUs for the LC VM |
| `lc:` | `ram` | RAM (GB) for the LC VM |
| `lc:` | `max_nics` | Maximum NICs for the LC |
| `lc:` | `chassis` | Chassis type (must match CP) |
| `lc:` | `slot` | IOM slot number |
| `lc:` | `card` | IOM card type (iom4-e, etc.) |
| `lc:` | `mda/<N>` | MDA type in slot N |

### Example Multi-Linecard Topology

```yaml
apiVersion: clabernetes.containerlab.dev/v1alpha1
kind: Topology
metadata:
  name: sros-multilinecard
spec:
  deployment:
    resources:
      sr7-1:
        requests:
          memory: "12Gi"
          cpu: "6"
        limits:
          memory: "18Gi"
          cpu: "10"
  definition:
    containerlab: |
      name: sros-multilinecard
      topology:
        kinds:
          nokia_sros:
            image: vrnetlab/nokia_sros:24.7.R1
            license: /opt/nokia/sros/license.txt
        nodes:
          sr7-1:
            kind: nokia_sros
            type: "cp: cpu=4 ram=6 chassis=sr-7 slot=A card=cpm5 ___lc: cpu=4 ram=4 max_nics=36 chassis=sr-7 slot=1 card=iom4-e mda/1=me12-100gb-qsfp28"
          peer-srl:
            kind: nokia_srlinux
            image: ghcr.io/nokia/srlinux:latest
        links:
          - endpoints: ["sr7-1:eth1", "peer-srl:e1-1"]
```

### Resource Sizing for Multi-Linecard

Each QEMU VM inside the container consumes the CPU and RAM defined in the type string. The container resource limits must accommodate **all** VMs:

| Component | Typical CPU | Typical RAM |
|-----------|-------------|-------------|
| CP VM | 2-4 cores | 4-6 GB |
| LC VM (each) | 2-4 cores | 4 GB |
| Container overhead | 1 core | 1 GB |

**Formula:** `total = CP + (N × LC) + overhead`

For an SR-7 with 1 CP and 1 LC: ~6 CPUs, ~12 GB RAM minimum.

## Comparison: nokia_sros vs nokia_srsim

| Aspect | `nokia_sros` (vrnetlab) | `nokia_srsim` |
|--------|------------------------|---------------|
| Runtime | QEMU VM in container | Native container |
| Multi-linecard | All VMs in ONE container | Separate containers per card |
| Grouping | Single node, no `network-mode` | `network-mode: container:<primary>` |
| Image source | srl-labs/vrnetlab + .qcow2 | Nokia Support Portal |
| Resource model | Container holds all VMs | Resources per container/card |
| Type format | `cp:/lc:` string | Simple type (sr-7, sr-14s) |
| License | Optional (30-min boot without) | Required |

**Both kinds can coexist in the same topology.** Clabernetes handles them independently — `nokia_sros` nodes are standalone, while `nokia_srsim` distributed nodes use network-mode grouping.

## Limitations

### Resource Allocation

All QEMU VMs share the single container's cgroup limits. If the container's memory limit is too low, QEMU VMs will be OOM-killed. Always size container resources to accommodate all VMs plus overhead.

### QEMU Monitor Ports

The CP uses QEMU monitor port 4000, and LCs use 4001+. These are internal to the container and do not need to be exposed, but be aware of them when debugging.

### Boot Time

Multi-linecard types take longer to boot because multiple QEMU VMs must start sequentially. Allow 10-20 minutes for a full SR-7 to become operational. Set `startupTimeoutSeconds` accordingly.

## Related Resources

- [srl-labs/vrnetlab](https://github.com/srl-labs/vrnetlab) — Build vrnetlab container images
- [Nokia SR-SIM Guide](./nokia-srsim.md) — For the native containerized simulator
- [Resource Management Guide](./resource-management.md)
- [File Mounting Guide](./file-mounting.md) — For license file mounting
