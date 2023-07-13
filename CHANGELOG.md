# Change Log

## v0.4.0
### Kruise-Rollout-Controller
- Rollout Support Kruise Advanced DaemonSet. ([#134](https://github.com/openkruise/rollouts/pull/134), [@Yadan-Wei](https://github.com/Yadan-Wei))
- Rollout support end-to-end canary deployment. ([#153](https://github.com/openkruise/rollouts/pull/153), [@zmberg](https://github.com/zmberg))
- Rollout trafficTouting support requestHeaderModifier. ([#156](https://github.com/openkruise/rollouts/pull/156), [@zmberg](https://github.com/zmberg))
- Rollout support disabled for a rollout. ([#155](https://github.com/openkruise/rollouts/pull/155), [@Kuromesi](https://github.com/Kuromesi))
- Rollout support patch PodTemplateMetadata. ([#157](https://github.com/openkruise/rollouts/pull/157), [@zmberg](https://github.com/zmberg))
- Rollout only webhook workload which has rollout CR. ([#158](https://github.com/openkruise/rollouts/pull/158), [@zmberg](https://github.com/zmberg))
- Advanced deployment scale down old unhealthy pods firstly. ([#150](https://github.com/openkruise/rollouts/pull/150), [@veophi](https://github.com/veophi))
- Update k8s registry references to registry.k8s.io. ([#126](https://github.com/openkruise/rollouts/pull/126), [@asa3311](https://github.com/asa3311))
- When the data type of spec.replicas is int, cancel the upper 100 limit. ([#142](https://github.com/openkruise/rollouts/pull/142), [@MrSumeng](https://github.com/MrSumeng))
- Add e2e test for advanced daemonSet. ([#143](https://github.com/openkruise/rollouts/pull/143), [@Janice1457](https://github.com/Janice1457))
- Exclude workload deleted matching labels in webhook. ([#146](https://github.com/openkruise/rollouts/pull/146), [@wangyikewxgm](https://github.com/wangyikewxgm))
- Optimize the modification of rollout to GatewayAPI httpRoute header. ([#137](https://github.com/openkruise/rollouts/pull/137), [@ZhangSetSail](https://github.com/ZhangSetSail))

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
