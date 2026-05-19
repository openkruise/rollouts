# Deployment MinReadySeconds Runbook

This page is for operators who need to inspect, support, or recover a MinReadySeconds rollout.

## Quick status map

| Condition | Meaning | Operator action |
|-----------|---------|-----------------|
| `MinReadyInitialized` | Original values are stored and live fields are inflated | Start watching batches |
| `MinReadyBatching` | A batch is in progress or waiting on readiness | Inspect pods and rollout status |
| `MinReadyDegraded` | The controller stopped on an explicit blocking issue | Follow the relevant recovery path |
| `MinReadyFinalized` | The original Deployment fields were restored | No action needed |

## Normal lifecycle

1. `Initialize` stores the original Deployment fields in annotations.
2. The controller inflates rollout fields and advances each batch.
3. `Finalize` restores the original fields and removes the annotations.

The rollout status uses these conditions:

- `MinReadyInitialized`
- `MinReadyBatching`
- `MinReadyDegraded`
- `MinReadyFinalized`

## Common events

- `MinReadyInitialized`
- `MinReadyBatchUpgraded`
- `MinReadyFinalized`
- `MinReadyDegradedMissingAnnotations`
- `MinReadyDegradedDriftDetected`
- `MinReadyDegradedPDBIncompatible`
- `MinReadyFeatureGateDisabled`

The current implementation does not emit a dedicated `MinReadyBatchStuck` event. Use the `MinReadyBatching` condition together with `rollout_minready_stuck_seconds` to detect long waits.

## Degraded states

`MinReadyDegraded` means the controller stopped because the rollout cannot safely continue.

Typical causes:

- feature gate disabled
- original annotations missing or partial
- live Deployment fields no longer match the inflated MinReadySeconds state
- PDB selector matches the Deployment pods

## Troubleshooting matrix

| Reason | Diagnostic command | What to look for |
|--------|--------------------|------------------|
| Feature gate disabled | `kubectl describe rollout <name>` | Warning event with `MinReadyFeatureGateDisabled` |
| Missing annotations | `kubectl get deploy <name> -o yaml` | One or more original annotations absent |
| Drift detected | `kubectl get deploy <name> -o yaml` | `minReadySeconds`, `progressDeadlineSeconds`, or `maxSurge` no longer match inflated values |
| PDB conflict | `kubectl get pdb -n <namespace> -o yaml` | Selector matches the workload labels |
| Batch waiting too long | `kubectl get rollout <name> -o yaml` and metrics | `MinReadyBatching` stays true and `rollout_minready_stuck_seconds` remains above zero |

## PDB incompatibility

If a matching PodDisruptionBudget exists for the target Deployment, initialization is rejected.

This is intentional in alpha. Do not try to work around it by manually forcing batch progression.

## Break-glass flow

Use this when the rollout is degraded and you need to recover quickly:

1. Identify the blocking reason from events and conditions.
2. Fix the root cause instead of patching around it.
3. Reconcile the Rollout again.
4. Confirm the rollout returns to `MinReadyInitialized`, `MinReadyBatching`, or `MinReadyFinalized`.

If the original annotations were removed accidentally, restore them before the next reconcile. Do not write default values by hand unless the original values were truly default.

## Drift detection

The controller treats these as drift:

- `spec.minReadySeconds` no longer equals the inflated value
- `spec.progressDeadlineSeconds` no longer equals the inflated value
- `spec.strategy.rollingUpdate.maxSurge` is no longer `0`
- original annotations are partially missing

When drift is detected, the rollout enters `MinReadyDegraded` and emits `MinReadyDegradedDriftDetected`.

## Inspecting a live rollout

```bash
kubectl get rollout <name> -o yaml
kubectl describe rollout <name>
kubectl get deploy <name> -o yaml
kubectl get pdb -n <namespace>
```

Check these fields:

- annotations under `metadata.annotations`
- `status.conditions`
- the current batch and replica counts

## Recovery

Recovery depends on the cause.

- If the feature gate was disabled, enable `MinReadySecondsStrategy` and retry the rollout.
- If a PDB matches, remove the PDB or move the workload to a non-overlapping selector.
- If annotations are missing or the live fields drifted, treat the Deployment as damaged and re-create the rollout state from the desired spec.
- If the rollout is waiting on batch readiness, inspect `status.conditions`, the Deployment replica counts, and the current batch in the Rollout status before taking action.

## Finalization

Finalize restores:

- `minReadySeconds`
- `progressDeadlineSeconds`
- `maxUnavailable`
- `maxSurge`

If finalize fails, the controller reports `MinReadyDegraded` and keeps the annotations until the blocking issue is resolved.

## Common log patterns

- `MinReadyControl.Initialize` failures usually point to feature gate, PDB, or annotation problems.
- `MinReadyControl.UpgradeBatch[...]` failures usually point to drift or stale workload state.
- `MinReadyControl.Finalize` failures usually point to missing annotations or malformed annotation values.

## Monitoring suggestions

- Alert when `MinReadyDegraded` stays true for more than one reconcile window.
- Alert when `rollout_minready_degraded_total` increases for the same rollout.
- Track `rollout_minready_batch_duration_seconds` for batch completion latency.
- Track `rollout_minready_stuck_seconds` to spot batches that are still waiting on readiness.
