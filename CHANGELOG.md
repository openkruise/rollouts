# Change Log

## v0.3.0

### Kruise-Rollout-Controller
#### New Features:
- Support rolling update deployment in batches without extra canary deployment.
- Support A/B Testing traffic routing.
- Support various types of traffic routing via adding Lua scripts in a pluggable way.
- Support [Higress](https://higress.io/en-us/) traffic routing.
- Support failure toleration threshold for rollout.
- Support multi-architectures, such as x86 and arm.
#### Optimization:
- Optimize rollout/batchRelease controller implementation.
- Allow users define the number of goroutines of controller.
- Add `UserAgent = kruise-rollout` for kruise-rollout operator.
- Define `rollout-id` in workload instead of rollout to avoid race bug.

## v0.2.0
### Kruise-Rollout-Controller
- Rollout Support StatefulSet & Advanced StatefulSet.
- Support patch batch-id label to pods during Rollout.
- Support the Gateway API for the canary release.

## v0.1.0
### Kruise-Rollout-Controller
- Support Canary Publishing + Nginx Ingress + Workload(CloneSet, Deployment).
- Support for Batch Release(e.g. 20%, 40%, 60%, 80, 100%) for workload(CloneSet).

### Documents
- Introduction, Installation, Basic Usage
