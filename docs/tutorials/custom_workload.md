# Progressive Delivery for Custom (StatefulSet-like) Workloads

Kruise Rollout natively supports `Deployment`, `CloneSet`, `StatefulSet`, `Advanced StatefulSet`, and `DaemonSet`.
Beyond these, it also supports **any custom CRD that follows the StatefulSet update model** — i.e., CRDs that use a
`spec.updateStrategy.rollingUpdate.partition` field to control rolling updates.

This is useful for workloads managed by third-party operators such as:
- [Elasticsearch](https://github.com/elastic/cloud-on-k8s) (`Elasticsearch` CRD)
- [OpenKruise Advanced StatefulSet-like CRDs](https://openkruise.io)
- Any operator whose CR has `spec.replicas`, `spec.template`, and `spec.updateStrategy.rollingUpdate.partition`

---

## How It Works

When a Rollout is created for a custom workload, Kruise Rollout patches its admission webhook to watch that workload's
GroupVersionKind. On every update to the custom workload, the webhook checks for the
`rollouts.kruise.io/workload-type: statefulset` label. If the label is present, the webhook intercepts the update and
sets `spec.updateStrategy.rollingUpdate.partition` to a large value to pause the rollout. The Rollout controller then
progressively adjusts the partition according to the configured batch strategy.

---

## Requirements

The custom CRD must satisfy the following structural requirements:

| Field | Type | Purpose |
|---|---|---|
| `spec.replicas` | `integer` | Total number of replicas |
| `spec.template.metadata` | `object` | Pod template metadata |
| `spec.template.spec` | `object` | Pod template spec |
| `spec.updateStrategy.type` | `string` | Must be `""` or `"RollingUpdate"` |
| `spec.updateStrategy.rollingUpdate.partition` | `integer` | Kruise Rollout uses this to pause and advance the rollout |

If the CRD does not have the `partition` field or does not honour it during rolling updates, the rollout will not work correctly.

---

## Step 1 — Add the Label to Your Custom Workload

Add the label `rollouts.kruise.io/workload-type: statefulset` to your custom workload resource. This tells Kruise
Rollout to treat it as a StatefulSet-like workload:

```yaml
apiVersion: example.io/v1
kind: MyStatefulApp
metadata:
  name: my-app
  namespace: default
  labels:
    rollouts.kruise.io/workload-type: statefulset   # required
spec:
  replicas: 5
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      partition: 0
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
        - name: app
          image: my-registry/my-app:v1
```

> **Note:** The label value must be `statefulset` (lowercase). Other values (`deployment`, `cloneset`, `daemonset`)
> are reserved for natively supported workload types.

---

## Step 2 — Create a Rollout for the Custom Workload

Reference the custom CRD in the Rollout's `workloadRef` using its `apiVersion` and `kind`:

```yaml
apiVersion: rollouts.kruise.io/v1beta1
kind: Rollout
metadata:
  name: my-app-rollout
  namespace: default
spec:
  workloadRef:
    apiVersion: example.io/v1        # apiVersion of your custom CRD
    kind: MyStatefulApp              # Kind of your custom CRD
    name: my-app                     # name of the specific instance
  strategy:
    canary:
      steps:
        - replicas: 1                # Step 1: update 1 replica, then pause for manual approval
          pause: {}
        - replicas: 50%              # Step 2: update 50% of replicas
          pause:
            duration: 300            # Auto-advance after 5 minutes
        - replicas: 100%             # Step 3: complete the rollout
```

> **Supported strategy:** Only multi-batch (canary with `steps`) is supported for custom workloads. Traffic routing
> (`trafficRoutings`) is not supported because Kruise Rollout does not manage the networking layer for custom CRDs.

---

## Step 3 — Trigger a Rollout

Update the Pod template of your custom workload (e.g., bump the image tag):

```bash
kubectl patch mystatefulapp my-app -n default \
  --type=merge \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"my-registry/my-app:v2"}]}}}}'
```

Kruise Rollout intercepts the update, pauses it, and starts the batch strategy. Check the rollout status:

```bash
kubectl get rollout my-app-rollout -n default -o yaml
```

When the rollout is paused at a step, advance it manually:

```bash
kubectl-kruise rollout approve rollout/my-app-rollout -n default
```

---

## Step 4 — Rollback

To roll back, simply revert the workload template to the previous image or configuration and apply it:

```bash
kubectl patch mystatefulapp my-app -n default \
  --type=merge \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"my-registry/my-app:v1"}]}}}}'
```

Kruise Rollout detects the rollback and completes it without going through the batch steps.

---

## Troubleshooting

**The Rollout status stays `Disabled` after creating the Rollout.**

Check that the `rollouts.kruise.io/workload-type: statefulset` label is present on the workload. Without it, the
admission webhook will not intercept updates to the CRD.

**The partition is not being set on my custom workload.**

Verify that your CRD's schema defines `spec.updateStrategy.rollingUpdate.partition` as an integer field, and that your
operator controller honours this field when reconciling rolling updates.

**The rollout never advances past the first step.**

If the step has `pause: {}` (indefinite pause), you must manually approve it with `kubectl-kruise rollout approve`.
If the step has a `duration`, ensure the Kruise Rollout controller pod is running and healthy.
