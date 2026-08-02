---
title: RS-Based Revision Stability for Deployment Rollouts
authors:
  - "qoder@openkruise.io"
reviewers:
  - "@furykerry"
  - "@AiRanthem"
  - "@zmberg"
creation-date: 2026-08-02
last-updated: 2026-08-02
status: provisional
---

# RS-Based Revision Stability for Deployment Rollouts

## Table of Contents

- [RS-Based Revision Stability for Deployment Rollouts](#rs-based-revision-stability-for-deployment-rollouts)
    - [Table of Contents](#table-of-contents)
    - [Glossary](#glossary)
    - [Summary](#summary)
    - [Motivation](#motivation)
        - [Goals](#goals)
        - [Non-Goals](#non-goals)
    - [Proposal](#proposal)
        - [Problem Analysis](#problem-analysis)
            - [Root Cause](#root-cause)
            - [Affected Code Paths](#affected-code-paths)
            - [Affected Revision Consumers](#affected-revision-consumers)
        - [Implementation Details](#implementation-details)
            - [Native Deployment Path](#native-deployment-path)
            - [Advanced Deployment Path](#advanced-deployment-path)
            - [Revision Comparison Logic](#revision-comparison-logic)
            - [Rollout-ID Computation](#rollout-id-computation)
            - [Migration: Lazy Revision Normalization](#migration-lazy-revision-normalization)
                - [Detection](#detection)
                - [Verification via EqualIgnoreHash](#verification-via-equalignorehash)
                - [Normalization](#normalization)
                - [Real Continuous Release](#real-continuous-release)
            - [Fallback Chain](#fallback-chain)
        - [Risks and Mitigations](#risks-and-mitigations)
    - [Alternatives](#alternatives)
        - [JSON-Based Hash](#json-based-hash)
        - [Webhook-Injected Revision Annotation](#webhook-injected-revision-annotation)
        - [Template-Based Semantic Comparison](#template-based-semantic-comparison)
    - [Upgrade Strategy](#upgrade-strategy)
    - [Implementation History](#implementation-history)

## Glossary

- **`pod-template-hash`**: A label automatically computed and persisted by the in-cluster Kubernetes Deployment controller on each ReplicaSet it creates. The label value is determined at ReplicaSet creation time and does not change thereafter, even if the controller binary or the cluster is later upgraded.
- **`ComputeHash`**: A function in the Kruise Rollouts codebase (`pkg/util/workloads_utils.go`) that computes a hash from a `PodTemplateSpec` using the `spew` library's `%#v` format. Because `spew` serializes every Go struct field including zero-valued ones, the hash output changes whenever the k8s API Go library adds a new field to `PodTemplateSpec`.
- **`CanaryRevision`**: A string stored in `Rollout.Status.CanaryStatus.CanaryRevision` (or `BlueGreenStatus.UpdatedRevision`) that identifies the canary revision of the current rollout. It is compared across reconciles to detect continuous releases, rollbacks, and rollout completion.
- **`Continuous Release`**: A scenario in which a user pushes a new pod-template version (e.g., v3) while a previous rollout (v1 to v2) is still in progress. The controller detects this by comparing the current workload's canary revision with the one stored in status.
- **`EqualIgnoreHash`**: A utility function (`pkg/util/workloads_utils.go`) that compares two `PodTemplateSpec` objects using `apiequality.Semantic.DeepEqual` after stripping the `pod-template-hash` label. Semantic deep equality treats nil and empty slices as equal, making it robust against Go struct field additions across k8s versions.
- **`FindCanaryAndStableReplicaSet`**: A utility function that sorts a Deployment's ReplicaSets by revision and returns the canary ReplicaSet (whose template matches the Deployment) and the stable ReplicaSet (the first active ReplicaSet with replicas > 0 whose template does not match).
- **Revision Drift**: The phenomenon where the `CanaryRevision` value recomputed at reconcile time differs from the value stored in `Rollout.Status` during a previous reconcile, even though the underlying pod template has not changed. This is the root cause of the false-positive continuous-release detection after a controller upgrade.

## Summary

The Kruise Rollouts controller computes the `CanaryRevision` for native and advanced Deployments by calling `ComputeHash` on the Deployment's pod template at every reconcile. This hash function uses the `spew` library to serialize the entire `PodTemplateSpec` Go struct, including all zero-valued fields. When the project upgrades its Kubernetes Go dependency (for example from 1.24 to 1.26, where `schedulingGates` was added to `PodTemplateSpec`), the hash of the exact same pod template changes. Because the controller compares this recomputed hash against the value persisted in `Rollout.Status.CanaryRevision` during a prior reconcile, the mismatch triggers a false-positive continuous-release detection, which resets the rollout to step 1, aborts an in-flight rollout, or corrupts the rollback state. The failure manifests only for Deployment workloads — CloneSet, DaemonSet, and StatefulSet already source their canary revision from the workload's `.Status.UpdateRevision`, which is computed by the in-cluster controller and is therefore stable across Kruise Rollouts binary upgrades.

This proposal replaces the `ComputeHash`-based `CanaryRevision` with a value read from the ReplicaSet's `pod-template-hash` label. The ReplicaSet label is computed and persisted once by the in-cluster Kubernetes Deployment controller at ReplicaSet creation time; it is never recomputed by the Kruise Rollouts binary and therefore does not drift across controller upgrades. The existing revision-comparison logic in `isContinuousRelease`, `isRollingBackDirectly`, and `isRollingBackInBatches` requires no modification — once `CanaryRevision` is sourced from a stable value, the comparisons naturally produce correct results. A one-time lazy migration using `EqualIgnoreHash` handles the transition for in-progress rollouts whose stored `CanaryRevision` was computed by the old `ComputeHash` method.

## Motivation

### Goals

- Eliminate false-positive continuous-release, rollback, and abort detections caused by Kruise Rollouts binary upgrades for Deployment workloads.
- Source `CanaryRevision` from the ReplicaSet `pod-template-hash` label instead of runtime `ComputeHash` recomputation, making the revision stable across controller upgrades.
- Require no API or CRD changes — the proposal is purely an internal implementation change.
- Require no webhook changes — the webhook already uses `EqualIgnoreHash` for revision-change detection and is unaffected.
- Provide a safe, one-time lazy migration for in-progress rollouts that bridges the gap between the old `ComputeHash`-based value and the new RS-label-based value.
- Preserve the existing revision-comparison logic (`isContinuousRelease`, `isRollingBackDirectly`, `isRollingBackInBatches`) without modification.

### Non-Goals

- **Changing the revision source for CloneSet, DaemonSet, or StatefulSet**: These workload types already source `CanaryRevision` from `.Status.UpdateRevision`, which is stable. They are out of scope.
- **Eliminating `ComputeHash` entirely**: `ComputeHash` is retained as a fallback for the non-progressing path and the brief window before a new ReplicaSet is created. It is only removed from the comparison-critical path.
- **Modifying the webhook**: The webhook already uses `EqualIgnoreHash` (semantic comparison) and does not call `ComputeHash`. No webhook changes are needed.
- **Adding new API fields**: No new Rollout, BatchRelease, or CRD fields are introduced.
- **Solving cluster-level k8s upgrades**: If the cluster itself is upgraded, newly created ReplicaSets will have different `pod-template-hash` labels. This is correct behavior (the template effectively changed from the cluster's perspective) and does not cause false positives because only newly created ReplicaSets are affected — existing ones retain their original labels.

## Proposal

### Problem Analysis

#### Root Cause

The `ComputeHash` function in `pkg/util/workloads_utils.go` serializes the entire `PodTemplateSpec` using `spew.Fprintf(hasher, "%#v", *template)`. The `spew` library's `%#v` verb prints every Go struct field, including zero-valued fields. When the k8s Go dependency is upgraded and a new field is added to `PodTemplateSpec` (e.g., `schedulingGates` in k8s 1.26), the `spew` output for the same logical template changes, producing a different hash.

The `CanaryRevision` for Deployment workloads is computed via `ComputeHash` at every reconcile:

```go
// pkg/util/controller_finder.go, getNativeDeployment
workload := &Workload{
    CanaryRevision: ComputeHash(&deployment.Spec.Template, nil),
    ...
}
```

The `isContinuousRelease` function compares this recomputed value against the one stored in `Rollout.Status`:

```go
// pkg/controller/rollout/rollout_progressing.go
func isContinuousRelease(rollout *v1beta1.Rollout, workload *util.Workload) bool {
    status := &rollout.Status
    return status.GetCanaryRevision() != "" &&
           workload.CanaryRevision != status.GetCanaryRevision() &&
           !workload.IsInRollback
}
```

After a controller binary upgrade, `workload.CanaryRevision` (newly computed) differs from `status.GetCanaryRevision()` (stored before upgrade), even if the pod template is unchanged. This triggers a false-positive continuous-release detection, which calls `handleContinuousRelease` → `doProgressingReset` → resets `CurrentStepIndex` to 1 and clears sub-status.

#### Affected Code Paths

Only the two Deployment paths in `pkg/util/controller_finder.go` are affected:

| Function | Line | Current `CanaryRevision` Source |
|---|---|---|
| `getNativeDeployment` (native Deployment) | ~L326 | `ComputeHash(&deployment.Spec.Template, nil)` |
| `getDeployment` (advanced Deployment with canary clone) | ~L386 | `ComputeHash(&stable.Spec.Template, nil)` |

All other workload types already use status-based revisions:

| Workload | Source | Stable? |
|---|---|---|
| CloneSet | `cloneSet.Status.UpdateRevision` | Yes |
| Kruise DaemonSet | `daemonSet.Status.DaemonSetHash` | Yes |
| Native DaemonSet | `workloadInfo.Status.UpdateRevision` | Yes |
| StatefulSet-like | `workloadInfo.Status.UpdateRevision` | Yes |

#### Affected Revision Consumers

Three comparison sites consume `CanaryRevision` and are affected by revision drift:

1. **`isContinuousRelease`** (`rollout_progressing.go:428`): Detects continuous release by comparing `workload.CanaryRevision != status.GetCanaryRevision()`. False positive after upgrade → resets to step 1.

2. **`isRollingBackDirectly` / `isRollingBackInBatches`** (`rollout_progressing.go:433, 439`): Detects rollback by the same comparison plus `workload.IsInRollback`. False positive after upgrade → corrupts rollback state.

3. **`ObservedRolloutID` update** (`rollout_status.go:97-98`): Updates `ObservedRolloutID` only when `newStatus.GetCanaryRevision() == workload.CanaryRevision`. False mismatch after upgrade → `ObservedRolloutID` not updated → PaaS platform cannot detect rollout completion.

### Implementation Details

#### Native Deployment Path

In `getNativeDeployment` (`pkg/util/controller_finder.go`), the current code computes `CanaryRevision` via `ComputeHash` unconditionally. The change makes `CanaryRevision` sourced from the ReplicaSet label when the workload is in rollout progressing:

Before:

```go
workload := &Workload{
    CanaryRevision: ComputeHash(&deployment.Spec.Template, nil),
    ...
}
// not in rollout progressing
if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
    return workload, nil
}
// in rollout progressing
rss, err := r.GetReplicaSetsForDeployment(deployment)
newRS, _ := FindCanaryAndStableReplicaSet(rss, deployment)
if newRS != nil {
    workload.PodTemplateHash = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
}
```

After:

```go
workload := &Workload{
    // Non-progressing: ComputeHash is acceptable (not used for cross-reconcile comparison)
    CanaryRevision: ComputeHash(&deployment.Spec.Template, nil),
    ...
}
// not in rollout progressing
if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
    return workload, nil
}
// in rollout progressing — source CanaryRevision from RS label
rss, err := r.GetReplicaSetsForDeployment(deployment)
if err != nil {
    return &Workload{IsStatusConsistent: false}, err
}
newRS, _ := FindCanaryAndStableReplicaSet(rss, deployment)
if newRS != nil {
    // ★ CanaryRevision from RS label — stable across controller upgrades
    workload.CanaryRevision = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
    workload.PodTemplateHash = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
}
// rollback detection: both values are RS labels, stable
if workload.StableRevision != "" && workload.StableRevision == workload.PodTemplateHash {
    workload.IsInRollback = true
}
workload.InRolloutProgressing = true
return workload, nil
```

The key change: `CanaryRevision` is overwritten with the RS label value when in rollout progressing. When the RS does not exist yet (brief window before the Deployment controller creates it), `CanaryRevision` retains the `ComputeHash` value as a temporary fallback.

#### Advanced Deployment Path

In `getDeployment` (`pkg/util/controller_finder.go`), the current code computes `CanaryRevision` from the stable Deployment's template via `ComputeHash`. The change sources it from the ReplicaSet label:

Before:

```go
stableRs, err := r.GetDeploymentStableRs(stable)
workload := &Workload{
    StableRevision: stableRs.Labels[apps.DefaultDeploymentUniqueLabelKey],
    CanaryRevision: ComputeHash(&stable.Spec.Template, nil),
    ...
}
// canary deployment
canary, err := r.getLatestCanaryDeployment(stable)
canaryRs, err := r.GetDeploymentStableRs(canary)
workload.PodTemplateHash = canaryRs.Labels[apps.DefaultDeploymentUniqueLabelKey]
```

After:

```go
stableRs, err := r.GetDeploymentStableRs(stable)
workload := &Workload{
    StableRevision: stableRs.Labels[apps.DefaultDeploymentUniqueLabelKey],
    // ★ Use stable RS label as initial value (represents the current deployment revision)
    CanaryRevision:   stableRs.Labels[apps.DefaultDeploymentUniqueLabelKey],
    ...
}
// Find the RS matching the current deployment template
rss, err := r.GetReplicaSetsForDeployment(stable)
newRS, _ := FindCanaryAndStableReplicaSet(rss, stable)
if newRS != nil {
    // ★ Prefer the canary RS label (matches current template)
    workload.CanaryRevision = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
    workload.PodTemplateHash = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
} else {
    // Fallback: use canary deployment's RS label
    canary, err := r.getLatestCanaryDeployment(stable)
    if err != nil || canary == nil {
        return workload, err
    }
    canaryRs, err := r.GetDeploymentStableRs(canary)
    if err != nil || canaryRs == nil {
        return workload, err
    }
    workload.PodTemplateHash = canaryRs.Labels[apps.DefaultDeploymentUniqueLabelKey]
}
```

#### Revision Comparison Logic

No changes are required to the three comparison sites:

| Function | File:Line | Comparison | Why It Works |
|---|---|---|---|
| `isContinuousRelease` | `rollout_progressing.go:428` | `workload.CanaryRevision != status.GetCanaryRevision()` | Both values now come from RS labels; same RS → same label → no false positive. Different RS (new version) → different label → correct detection. |
| `isRollingBackDirectly` | `rollout_progressing.go:433` | Same + `workload.IsInRollback` | `IsInRollback` is set via `StableRevision == PodTemplateHash`, both RS labels. |
| `isRollingBackInBatches` | `rollout_progressing.go:439` | Same + `IsRollbackInBatchPolicy` | Same reasoning. |

| Function | File:Line | Condition | Why It Works |
|---|---|---|---|
| `ObservedRolloutID` update | `rollout_status.go:97-98` | `newStatus.GetCanaryRevision() == workload.CanaryRevision` | Both RS-label-based → matches when template unchanged → `ObservedRolloutID` updated → PaaS completion check works. |

#### Rollout-ID Computation

`getRolloutID` (`rollout_status.go:357`) falls back to `workload.CanaryRevision` when `RolloutIDLabel` is empty. After the change, this fallback value is the RS label, which is stable across controller upgrades. No modification is needed.

#### Migration: Lazy Revision Normalization

When the controller binary is upgraded, in-progress rollouts have `status.CanaryRevision` computed by the old `ComputeHash` method. After the upgrade, `workload.CanaryRevision` comes from the RS label. The two values use different computation methods and will not match, causing a one-time false positive.

The migration is performed lazily on the first reconcile after upgrade, using `EqualIgnoreHash` to distinguish a real revision change from a computation-method change.

##### Detection

A migration is needed when all of the following are true:

1. The rollout is in `Progressing` phase.
2. `status.GetCanaryRevision()` is non-empty.
3. `workload.CanaryRevision != status.GetCanaryRevision()` (values differ).
4. `workload.InRolloutProgressing` is true (the Deployment has the progressing annotation).
5. `workload.IsInRollback` is false (rollback is detected via RS labels, which are stable).

When these conditions are met, the controller cannot distinguish between a real continuous release and a computation-method change. It must verify using `EqualIgnoreHash`.

##### Verification via EqualIgnoreHash

The controller fetches the Deployment and its ReplicaSets, then calls `FindCanaryAndStableReplicaSet` to obtain the canary ReplicaSet (the one whose template matches the current Deployment). It then calls `EqualIgnoreHash(&newRS.Spec.Template, &deployment.Spec.Template)`.

`EqualIgnoreHash` uses `apiequality.Semantic.DeepEqual`, which:
- Treats nil slices and empty slices as equal.
- Treats zero-valued struct fields as equal.
- Is robust against Go struct field additions across k8s versions.

The webhook already relies on this function for `isEffectiveDeploymentRevisionChange`, demonstrating its fitness for this purpose.

| `EqualIgnoreHash` Result | Interpretation | Action |
|---|---|---|
| `true` (templates match) | Pod template has not changed; the revision mismatch is a computation-method artifact | Normalize: set `status.CanaryRevision` to the RS label value |
| `false` (templates differ) | Pod template genuinely changed; this is a real continuous release | Proceed to `handleContinuousRelease` |
| RS not found | New ReplicaSet not yet created; cannot verify | Requeue with grace period |

##### Normalization

When `EqualIgnoreHash` confirms the template is unchanged:

1. Set `c.NewStatus.SetCanaryRevision(rsHash)` where `rsHash = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]`.
2. Set `c.NewStatus.GetSubStatus().ObservedRolloutID = getRolloutID(workload)` to refresh the rollout ID with the new format.
3. Proceed to `handleNormalRolling(c)` — continue the current step without resetting.

This is a one-time operation: on the next reconcile, `workload.CanaryRevision` (RS label) will match `status.CanaryRevision` (now also RS label), and the migration check will be skipped.

##### Real Continuous Release

When `EqualIgnoreHash` confirms the template has genuinely changed, the controller delegates to the existing `handleContinuousRelease` handler. This is the correct behavior — the user pushed a new version while the previous rollout was in progress.

The migration logic is inserted at the top of `doProgressingInRolling`, before the existing switch:

```go
func (r *RolloutReconciler) doProgressingInRolling(c *RolloutContext) error {
    // ★ Lazy migration for pre-upgrade workloads
    if r.needsRevisionMigration(c.Rollout, c.Workload) {
        return r.migrateCanaryRevision(c)
    }
    switch {
    case isRollingBackDirectly(c.Rollout, c.Workload):
    // ... existing cases unchanged ...
    }
    return r.handleNormalRolling(c)
}
```

#### Fallback Chain

The complete `CanaryRevision` resolution chain, from highest priority to lowest:

| Priority | Source | When Used | Stable Across Controller Upgrade? |
|---|---|---|---|
| 1 | ReplicaSet `pod-template-hash` label | Workload in rollout progressing, canary RS exists | Yes |
| 2 | `ComputeHash(&deployment.Spec.Template, nil)` | Workload not in progressing, or canary RS not yet created | No (acceptable — see below) |

Priority 2 is acceptable because:
- When not in rollout progressing, `CanaryRevision` is not compared against a stored status value (status is empty or the rollout hasn't started). It is only used for `getRolloutID` during the initial status population, which does not require cross-reconcile stability.
- When the canary RS is not yet created, the value is temporary. On the next reconcile (after the RS is created), it will be overwritten with the RS label.

### Risks and Mitigations

**Risk**: ReplicaSet not yet created when the controller reconciles.

When a user updates the Deployment template, the webhook intercepts and sets `paused=true`. The in-cluster Deployment controller then creates a new ReplicaSet. If the Kruise Rollouts controller reconciles before the new ReplicaSet exists, `FindCanaryAndStableReplicaSet` returns `newRS=nil`, and `CanaryRevision` retains the `ComputeHash` fallback value.

**Mitigation**: The fallback `ComputeHash` value is used for at most one reconcile cycle. The migration check detects the mismatch and, finding no RS to verify against, requeues with a grace period. On the next reconcile, the RS exists and `CanaryRevision` is correctly sourced from the label. No false-positive continuous release is triggered because the migration logic intercepts before the switch cases.

---

**Risk**: Cluster k8s version upgrade changes the `pod-template-hash` label format.

If the cluster is upgraded (e.g., k8s 1.24 → 1.26), newly created ReplicaSets will have different `pod-template-hash` labels (the in-cluster Deployment controller's hash function also uses `spew`). Existing ReplicaSets retain their original labels.

**Mitigation**: This is correct behavior, not a bug. When the cluster is upgraded and the user subsequently updates the Deployment template, a new ReplicaSet is created with a new-format label. The controller correctly detects this as a revision change (`newRS label != status.CanaryRevision`) and triggers the appropriate handler (continuous release or new release). No false positive occurs because existing ReplicaSets (and their labels) are not affected by the cluster upgrade.

---

**Risk**: Annotation tampering or manual ReplicaSet deletion removes the canary ReplicaSet.

If the canary ReplicaSet is manually deleted during a rollout, `FindCanaryAndStableReplicaSet` cannot find a matching ReplicaSet.

**Mitigation**: The fallback to `ComputeHash` provides a non-empty `CanaryRevision`. The migration check will requeue until the ReplicaSet is recreated by the in-cluster controller (which will recreate it because the Deployment template still differs from the stable RS). If the ReplicaSet cannot be recreated (e.g., the Deployment was deleted), the controller's existing finalization logic handles cleanup.

---

**Risk**: Migration logic adds overhead to every reconcile.

The migration check runs at the top of `doProgressingInRolling` on every reconcile while a rollout is in the `Progressing` phase.

**Mitigation**: The check short-circuits when `workload.CanaryRevision == status.GetCanaryRevision()` (the common case after migration is complete). The `EqualIgnoreHash` call only executes during the one-time migration window. After migration, the check is a single string comparison — negligible overhead.

## Alternatives

### JSON-Based Hash

Replace `spew.Fprintf(hasher, "%#v", ...)` with `json.Marshal(template)` + FNV hash. JSON serialization omits zero-valued fields with `omitempty` tags, making the hash more stable across k8s library upgrades.

**Rejected because**: The migration problem is unsolved. Existing `status.CanaryRevision` values were computed with `spew`; after switching to JSON, the new values will not match the old ones, causing the exact same false-positive continuous-release detection the proposal aims to fix. Additionally, JSON serialization is not guaranteed stable — fields without `omitempty` would serialize as `null`, and struct field declaration order affects output.

### Webhook-Injected Revision Annotation

Have the webhook write a generation-based revision annotation (`rollouts.kruise.io/release-revision: rev-<generation>`) on the Deployment at update time. The controller reads the annotation instead of recomputing a hash.

**Rejected for this proposal because**: While more fundamentally stable (generation does not depend on any hash algorithm), it requires webhook modifications and adds a new annotation key to the workload. The RS-based approach achieves the same goal with zero webhook changes and zero new annotations, by reusing the existing `pod-template-hash` label that the in-cluster controller already writes. The webhook-injected approach remains a viable future enhancement if cluster-level upgrade stability becomes a requirement.

### Template-Based Semantic Comparison

Replace all `CanaryRevision` string comparisons with direct `EqualIgnoreHash` calls on the pod templates. This would require storing the actual pod template (or a reference to it) in the Rollout status, rather than a hash string.

**Rejected because**: It requires API/CRD changes to store the template in status, which is a heavier change than necessary. The RS-based approach achieves the same stability by sourcing the hash string from a stable location (the RS label) rather than recomputing it.

## Upgrade Strategy

This feature is **additive and backward-compatible**:

- No new API fields, CRD changes, or webhook modifications are introduced.
- No feature gate is required — the change is an internal implementation detail that does not alter user-facing behavior.
- No v1alpha1/v1beta1 conversion change is required.
- The lazy migration handles in-progress rollouts automatically on the first reconcile after upgrade.
- Existing `Rollout` resources and their stored `CanaryRevision` values are not deleted or modified outside of the one-time normalization.
- `ComputeHash` and `DeepHashObject` are retained in the codebase as fallbacks; no existing callers are removed.

Users do not need to take any action. The controller binary upgrade is transparent.

## Implementation History

- [ ] 08/02/2026: Initial proposal draft submitted
- [ ] TBD: Implementation of `getNativeDeployment` and `getDeployment` RS-label sourcing
- [ ] TBD: Implementation of lazy migration logic in `doProgressingInRolling`
- [ ] TBD: Unit tests covering migration scenarios (upgrade mid-step-2, upgrade after completion, upgrade during rollback)
- [ ] TBD: E2E test: upgrade controller mid-rollout and verify step continuity
- [ ] TBD: E2E test: rollback after controller upgrade
