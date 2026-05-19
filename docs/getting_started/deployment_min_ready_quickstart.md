# Deployment MinReadySeconds Quickstart

This guide shows how to enable the native Deployment MinReadySeconds rollout strategy in Kruise Rollouts.

## What this strategy does

MinReadySeconds keeps native Kubernetes `Deployment.spec.strategy.type` unchanged and relies on inflated rollout fields plus Kruise Rollouts orchestration to advance batches. It is intended for users who want controlled, batch-based rollout behavior without switching the workload to Recreate.

The controller writes and later restores these original Deployment fields:

- `spec.minReadySeconds`
- `spec.progressDeadlineSeconds`
- `spec.strategy.rollingUpdate.maxUnavailable`
- `spec.strategy.rollingUpdate.maxSurge`

The feature gate is `MinReadySecondsStrategy` and it is disabled by default.

## When to use

Use this strategy when:

- you want native Deployment semantics to stay in place
- you need batch-based rollout control
- you want the controller to restore the original Deployment fields automatically

Do not use it when:

- a PodDisruptionBudget covers the target workload
- you need a traffic-routing canary instead of a native Deployment rollout
- you cannot tolerate long Ready-but-not-Available periods during rollout

## Before you start

- Kubernetes cluster with Kruise Rollouts installed
- A `Deployment` managed by a `Rollout`
- `MinReadySecondsStrategy=true` enabled in the controller feature gate
- No PodDisruptionBudget covering the target Deployment namespace and selector

If a matching PDB exists, initialization is rejected and the rollout enters `MinReadyDegraded`.

## Minimal rollout example

```yaml
apiVersion: rollouts.kruise.io/v1beta1
kind: Rollout
metadata:
  name: demo-rollout
spec:
  strategy:
    canary:
      deploymentStrategy: MinReadySeconds
      steps:
      - replicas: 20%
      - replicas: 50%
      - replicas: 100%
```

The associated Deployment should keep a normal RollingUpdate strategy. Kruise Rollouts will inflate the live fields during rollout and restore the original values on finalize.

Example workload:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo-deploy
spec:
  replicas: 3
  selector:
    matchLabels:
      app: demo
  template:
    metadata:
      labels:
        app: demo
    spec:
      containers:
      - name: app
        image: nginx:1.27
```

## Enable the feature gate

Set the controller feature gate to:

```bash
MinReadySecondsStrategy=true
```

Without this gate, the controller rejects MinReadySeconds rollouts and records a warning event.

## Five minute smoke test

1. Apply the Rollout and Deployment.
2. Enable `MinReadySecondsStrategy=true` on the controller.
3. Update the Deployment image.
4. Watch the Rollout status and the Deployment annotations.
5. Confirm the rollout eventually reaches `MinReadyFinalized`.

## Verify the rollout

After the rollout starts:

```bash
kubectl get rollout demo-rollout
kubectl get deploy demo-deploy -o yaml
kubectl describe rollout demo-rollout
```

Expected behavior:

- the Deployment gets annotated with the original rollout fields
- `minReadySeconds` is inflated to the MaxReadySeconds value
- `progressDeadlineSeconds` is inflated to the MaxProgressSeconds value
- `maxUnavailable` is driven batch-by-batch
- `maxSurge` is kept at `0`

## What to look for

- `MinReadyInitialized` means the Deployment was initialized successfully
- `MinReadyBatching` means batches are progressing
- `MinReadyFinalized` means the original Deployment fields were restored
- `MinReadyDegraded` means the controller hit a blocking condition

## Recreate comparison

Compared with the old Recreate-style behavior:

- MinReadySeconds does not change `Deployment.spec.strategy.type`
- original rollout fields are stored and restored explicitly
- batch progression is driven by readiness on inflated fields
- Recreate-style mutation is skipped when the feature is enabled

## FAQ

### How do I enable the feature gate?

Set `MinReadySecondsStrategy=true` on the controller.

### Why does the rollout fail immediately?

The most common reasons are a disabled feature gate or a matching PDB.

### Can I use this with a Service Mesh?

Yes, but only if the mesh does not rely on mutating the Deployment strategy type or blocking readiness in a way that conflicts with the inflated rollout fields.

### Why does Available stay false for a long time?

That is expected. The strategy intentionally inflates `minReadySeconds` so the controller can control rollout progression by batch.

## Notes

- The strategy does not modify `Deployment.spec.strategy.type`.
- PDB-covered workloads are blocked in alpha.
- Existing annotations are treated as live state. Missing or partial original annotations are not considered a success path.
