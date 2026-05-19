# Migration from Recreate to MinReadySeconds

This guide explains how to move an existing rollout from the legacy Recreate-based flow to the MinReadySeconds strategy.

## What changes

With MinReadySeconds:

- the Deployment stays on native RollingUpdate
- the controller uses inflated rollout fields instead of switching `strategy.type`
- original rollout fields are stored in annotations and restored later

This is different from the older Recreate behavior.

## Behavior comparison

| Topic | Recreate | MinReadySeconds |
|-------|----------|-----------------|
| `Deployment.spec.strategy.type` | Switched to Recreate during rollout | Left unchanged |
| Rollout control | Full stop-and-replace behavior | Batch-based progression |
| Original fields | Not restored from annotations | Stored and restored |
| PDB compatibility | Depends on workload flow | Blocked in alpha |
| Operational risk | Simpler but more invasive | Less invasive but stricter on readiness |

## Compatibility checklist

Before migrating, confirm:

- the controller feature gate `MinReadySecondsStrategy` is enabled
- the target workload is not covered by a matching PDB
- the rollout spec uses `deploymentStrategy: MinReadySeconds`
- the workload can tolerate long Ready-but-not-Available periods during rollout
- your GitOps tool will not continuously fight the inflated rollout fields
- HPA is either disabled for the rollout or understood well enough to accept batch recalculation

## Migration steps

1. Pick a single namespace for the first trial.
2. Update the Rollout spec to use `deploymentStrategy: MinReadySeconds`.
3. Enable the `MinReadySecondsStrategy` feature gate on the controller.
4. Reconcile the rollout once so the controller writes the original annotations.
5. Verify that the live Deployment fields are inflated.
6. Watch the rollout status until `MinReadyFinalized`.
7. Roll the change out to other namespaces only after the first one is stable.

## Expected controller behavior

The executor routes MinReadySeconds rollouts to the MinReady controller.
The webhook keeps the Deployment strategy type unchanged for this path.
The controller updates batch state by patching `maxUnavailable` only.

## Rollout plan for an existing service

If you are moving a production service:

- start with one non-critical namespace
- watch events and status conditions during the first rollout
- confirm the Deployment annotations are written and later removed
- verify that your GitOps reconciler is not reverting `maxUnavailable`
- verify that HPA is not introducing surprise replica swings during the test rollout

## Rollback

To roll back, switch the Rollout spec back to the Recreate strategy and let the controller reconcile the workload back to its original fields.

If the rollout is already degraded, resolve the blocking cause first:

- enable the feature gate if it was disabled
- remove the overlapping PDB
- repair missing or malformed original annotations
- restore live Deployment fields that drifted out of the inflated state

## Known limitations

- PDB-covered workloads are blocked in alpha.
- Any direct manual edit of the inflated Deployment fields can move the rollout into `MinReadyDegraded`.
- This migration is only appropriate when you want the controller to preserve the native Deployment strategy type.

## Notes

- Do not migrate workloads with a covering PDB unless the strategy is redesigned for that topology.
- Do not change `Deployment.spec.strategy.type` manually during the migration.
- Keep the original annotations intact until finalize completes.
