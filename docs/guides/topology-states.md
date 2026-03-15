# Topology States

## State Machine

```
[created] → deploying → running
                ↓           ↓
          deployfailed    degraded → running (if recovered)
                              ↓
                          destroying → [deleted]
                              ↓
                         destroyfailed (stuck)
```

---

## States

| State | What it means |
|-------|---------------|
| `deploying` | Nodes are starting up, not ready yet |
| `deployfailed` | A node crashed before the lab ever came up |
| `running` | All nodes are healthy |
| `degraded` | Was running, but a node went down |
| `destroying` | Delete was requested, waiting ~5s before cleanup |
| `destroyfailed` | Deletion got stuck, needs manual investigation |

---

## Key Rules

- `deployfailed` — never reached `running`. Initial startup failed.
- `degraded` — reached `running` before, then something broke. A regression.
- Once `running`, a node going down → `degraded` (not `deployfailed`).
- `destroying` is always visible for ~5 seconds before the object disappears.
