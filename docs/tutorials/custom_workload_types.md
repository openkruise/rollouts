# Custom Workload Types with Kruise Rollout

Kruise Rollout supports extending rollout capabilities to custom resources (CRs) by using the `rollouts.kruise.io/workload-type` label. This feature allows you to enable StatefulSet-like, Deployment-like, CloneSet-like, or DaemonSet-like rollout behavior for your custom workloads.

## Overview

By default, Kruise Rollout supports the following built-in workload types:
- Kubernetes native: `Deployment`, `StatefulSet`
- OpenKruise: `CloneSet`, `Advanced StatefulSet`, `Advanced DaemonSet`

However, you can extend this support to any custom resource by adding the appropriate workload type label.

## Supported Workload Type Labels

The `rollouts.kruise.io/workload-type` label accepts the following values:

| Label value | Behavior | Use case |
|-------------|----------|----------|
| `statefulset` | StatefulSet-like rolling updates with partition support | Ordered pod updates, persistent storage workloads |
| `deployment` | Deployment-like rolling updates | Stateless applications |
| `cloneset` | CloneSet-like rolling updates | Advanced pod management features |
| `daemonset` | DaemonSet-like rolling updates | Node-level services |

## Requirements for Custom Resources

For a custom resource to work with Kruise Rollout using the workload-type label, it must have:

### 1. Required Spec Fields
```yaml
spec:
  replicas: <integer>
  selector:
    matchLabels:
      <key>: <value>
  template:
    metadata:
      labels:
        <key>: <value>
    spec:
      containers: [...]
```

### 2. Required Status Fields
```yaml
status:
  replicas: <integer>
  readyReplicas: <integer>
  updatedReplicas: <integer>
  updatedReadyReplicas: <integer>
  availableReplicas: <integer>
  observedGeneration: <integer>
  updateRevision: <string>
  stableRevision: <string>
```

### 3. Update Strategy (for StatefulSet-like behavior)
```yaml
spec:
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      partition: <integer>
      paused: <boolean>
```

## Example: StatefulSet-like Custom Resource

Here's a complete example of how to use a custom resource with StatefulSet-like rollout behavior:

### Step 1: Define the Custom Resource Definition

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: customstatefulsets.example.com
spec:
  group: example.com
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              replicas:
                type: integer
              selector:
                type: object
              template:
                type: object
              updateStrategy:
                type: object
                properties:
                  rollingUpdate:
                    type: object
                    properties:
                      partition:
                        type: integer
                      paused:
                        type: boolean
          status:
            type: object
            properties:
              replicas:
                type: integer
              readyReplicas:
                type: integer
              updatedReplicas:
                type: integer
              # ...other status fields
  scope: Namespaced
  names:
    plural: customstatefulsets
    singular: customstatefulset
    kind: CustomStatefulSet
```

### Step 2: Create the Custom Resource with Workload Type Label

```yaml
apiVersion: example.com/v1
kind: CustomStatefulSet
metadata:
  name: my-custom-workload
  labels:
    # this label enables StatefulSet-like rollout behavior
    rollouts.kruise.io/workload-type: statefulset
spec:
  replicas: 5
  selector:
    matchLabels:
      app: my-custom-workload
  template:
    metadata:
      labels:
        app: my-custom-workload
    spec:
      containers:
      - name: app
        image: nginx:1.20
        ports:
        - containerPort: 80
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      partition: 0
      paused: false
```

### Step 3: Create the Rollout Configuration

```yaml
apiVersion: rollouts.kruise.io/v1beta1
kind: Rollout
metadata:
  name: my-custom-workload-rollout
spec:
  workloadRef:
    apiVersion: example.com/v1
    kind: CustomStatefulSet
    name: my-custom-workload
  strategy:
    canary:
      steps:
      - replicas: 20%
        pause: {}
      - replicas: 40%
        pause: {}
      - replicas: 60%
        pause: {}
      - replicas: 80%
        pause: {}
      - replicas: 100%
        pause: {}
```

## How It Works?

1. **Label detection**: Kruise Rollout detects the `rollouts.kruise.io/workload-type` label on your custom resource
2. **Webhook registration**: The rollout controller automatically registers webhooks for resources with this label
3. **Behavior mapping**: The controller applies the appropriate rollout behavior based on the label value
4. **Automatic patching**: If the label is missing, the controller can automatically patch it based on the workload kind

## Automatic Label Patching

Kruise Rollout can automatically patch the workload-type label for known workload types:

- `Deployment` -> `rollouts.kruise.io/workload-type: deployment`
- `StatefulSet` -> `rollouts.kruise.io/workload-type: statefulset`
- `CloneSet` -> `rollouts.kruise.io/workload-type: cloneset`
- `DaemonSet` -> `rollouts.kruise.io/workload-type: daemonset`

## Limitations

1. **Custom controller required**: Your custom resource needs a controller that implements the required spec and status fields
2. **Webhook compatibility**: The custom resource must be compatible with Kubernetes admission webhooks
3. **Field structure**: The resource must follow the expected field structure for the chosen workload type
4. **Update strategy**: For StatefulSet-like behavior, the resource must support partition-based rolling updates

## Troubleshooting

### Common Issues

1. **Webhook not triggered**: Ensure the `rollouts.kruise.io/workload-type` label is present on the resource
2. **Missing fields**: Verify that all required spec and status fields are implemented
3. **Controller conflicts**: Make sure your custom controller doesn't conflict with rollout operations

### Debugging

Check the rollout controller logs for detailed information:
```bash
kubectl logs -n kruise-rollout deployment/kruise-rollout-controller
```

