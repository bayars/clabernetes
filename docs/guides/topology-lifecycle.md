# Topology Lifecycle States

Every clabernetes `Topology` resource moves through a defined set of lifecycle states that
are visible in `kubectl get topologies` (the **State** column) and in the
`status.topologyState` field.

This document describes the complete state machine, what triggers each transition, and how
to use the states for monitoring, automation, and troubleshooting.

---

## Quick reference

```
kubectl get topologies -A
```

```
NAMESPACE   NAME       KIND           AGE   READY   STATE
app         my-lab     containerlab   2m    true    running
```

```
kubectl get topology my-lab -o jsonpath='{.status.topologyState}'
```

---

## State machine

```
                         ┌─────────────────────────────────────────────┐
                         │             spec / image change              │
                         ▼                                              │
[created] ──► deploying ──────────────────► running ──► degraded ──────┘
                  │                            │            │
                  │                            ▼            ▼
                  ▼                        destroying    destroying
             deployfailed                      │            │
                                               ▼            ▼
                                         destroyfailed  destroyfailed
```

---

## States

### `deploying`

The topology has been accepted by the controller and resources (deployments, services,
configmaps) are being created or updated, but one or more launcher pods have not yet
reported ready.

**Triggers:** topology created, or spec changed and a rolling update is in progress.

**Exits to:**
- `running` — all nodes become ready.
- `deployfailed` — a node enters a terminal failure (CrashLoopBackOff / pod Failed) before
  any previous `running` state was reached.

---

### `running`

All nodes in the topology have reported ready. The topology is fully operational.

**Triggers:** every node's startup and readiness probes pass.

**Exits to:**
- `degraded` — a node that was running becomes unready or starts crashing.
- `destroying` — the topology resource is deleted.

---

### `deployfailed`

One or more nodes entered a terminal failure state (CrashLoopBackOff or pod phase `Failed`)
before the topology **ever** reached `running`.

This state is distinct from `degraded`. `deployfailed` means the lab never came up
successfully on this attempt. Check the launcher pod logs for the failing node:

```bash
kubectl logs -n <namespace> -l clabernetes/topologyNode=<node-name> --tail=100
```

**Exits to:**
- `deploying` — after the spec is corrected (e.g. image name fixed) and the controller
  triggers a new rollout.

---

### `degraded`

The topology **was** `running` at some point but one or more nodes have since become unready
or started crashing. The distinction between `degraded` and `deployfailed` is important:

| State | Meaning |
|-------|---------|
| `deployfailed` | Never reached `running`. Initial bring-up failed. |
| `degraded` | Was `running`, then something went wrong. A regression. |

Kubernetes will attempt to restart crashed containers automatically (back-off capped at 5 m).
If the node recovers, the topology returns to `running` without manual intervention.

**Exits to:**
- `running` — all nodes recover.
- `destroying` — the topology is deleted while degraded.

---

### `destroying`

A delete request has been received for the topology (`kubectl delete topology ...`).

The controller holds this state for a short observability window (approximately 5 seconds)
before removing the finalizer and allowing Kubernetes GC to clean up all owned resources
(deployments, services, configmaps, PVCs).

This window exists specifically so that external controllers, scripts, or monitoring systems
have time to observe the `destroying` state before the object disappears.

**Exits to:**
- *(object deleted)* — the finalizer is removed and Kubernetes GC proceeds.
- `destroyfailed` — the finalizer removal patch failed (apiserver error, admission webhook
  rejection, etc.).

---

### `destroyfailed`

The controller attempted to remove the topology finalizer during deletion but the API call
failed. The object will remain in the cluster with `DeletionTimestamp` set until the
underlying issue is resolved.

**What to check:**
- `kubectl describe topology <name>` — look for recent events or conditions.
- Controller manager logs: `kubectl logs -n c9s -l app=clabernetes-manager --tail=200`.
- Admission webhooks that may be blocking the resource update.

Once the root cause is resolved the controller will retry automatically on the next
reconciliation cycle and the object will be deleted.

---

## Per-node probe statuses

In addition to the topology-level state, each node has fine-grained probe status available
in `status.nodeProbeStatuses`:

```bash
kubectl get topology my-lab -o jsonpath='{.status.nodeProbeStatuses}' | python3 -m json.tool
```

```json
{
  "srl1": {
    "livenessProbe": "passing",
    "readinessProbe": "passing",
    "startupProbe": "passing"
  },
  "srl2": {
    "livenessProbe": "failing",
    "readinessProbe": "failing",
    "startupProbe": "passing"
  }
}
```

| Probe | Source | Meaning when passing |
|-------|--------|----------------------|
| `startupProbe` | `pod.status.containerStatuses[0].started` | The lab node has initialised and written its status file. |
| `readinessProbe` | `pod.status.containerStatuses[0].ready` | The node is ready to accept traffic. |
| `livenessProbe` | Container state (Running vs CrashLoopBackOff) | The launcher container is alive and not crash-looping. |

Possible values for each probe field: `passing`, `failing`, `unknown`, `disabled`.

---

## Simplified node readiness

For a quick per-node summary without probe granularity, use `status.nodeReadiness`:

```bash
kubectl get topology my-lab -o jsonpath='{.status.nodeReadiness}'
```

```json
{"srl1": "ready", "srl2": "notready"}
```

| Value | Meaning |
|-------|---------|
| `ready` | Startup and readiness probes both passing. |
| `notready` | Pod exists but probes have not yet passed. |
| `unknown` | No deployment exists for this node. |
| `deploymentDisabled` | Parent topology has the `clabernetes/disableDeployments` label. |

---

## Watching state changes

Watch the state column live:

```bash
kubectl get topologies -A -w
```

Wait for a topology to reach `running`:

```bash
kubectl wait topology my-lab -n app \
  --for=jsonpath='{.status.topologyState}'=running \
  --timeout=300s
```

Check for a failed topology in a script:

```bash
state=$(kubectl get topology my-lab -n app -o jsonpath='{.status.topologyState}')
if [[ "$state" == "deployfailed" || "$state" == "degraded" ]]; then
  echo "Lab is unhealthy: $state"
fi
```
