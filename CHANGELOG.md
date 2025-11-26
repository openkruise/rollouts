# Change Log

## v0.6.2
### Bugfix:
- Fixed issue where partition deployments got stuck. ([#307](https://github.com/openkruise/rollouts/pull/307),[@AiRanthem](https://github.com/AiRanthem))
- Compatibility with Karmada when the MutateWebhook newObj UID is empty during workload update. ([#319](https://github.com/openkruise/rollouts/pull/319),[@ivan-cai](https://github.com/ivan-cai))

## v0.6.1
### Key Features:
- Introduced `rollout-batch-id` labeling for blue-green and canary style releases ([#250](https://github.com/openkruise/rollouts/pull/250),([#251](https://github.com/openkruise/rollouts/pull/251),[#261](https://github.com/openkruise/rollouts/pull/261),[@PersistentJZH](https://github.com/PersistentJZH),[@AiRanthem](https://github.com/AiRanthem)) 

### Bugfix:
- The order of object in BatchRelease event handler is fixed ([#265](https://github.com/openkruise/rollouts/pull/265),[@z760087139](https://github.com/z760087139))

## v0.5.1
### Bugfix:

- Fix Rollout v1alpha1 and v1beta1 version conversions with incorrectly converted partition fields. ([#200](https://github.com/openkruise/rollouts/pull/200),[@myname4423](https://github.com/myname4423))

## v0.6.0
### Key Features:
- 🎊 Support for blue-green style releases has been added ([#214](https://github.com/openkruise/rollouts/pull/214),[#229](https://github.com/openkruise/rollouts/pull/229),[#238](https://github.com/openkruise/rollouts/pull/238),[#220](https://github.com/openkruise/rollouts/pull/220),[@myname4423](https://github.com/myname4423))
- ⭐ Traffic loss during rollouts in certain scenarios is avoided ([#226](https://github.com/openkruise/rollouts/pull/226),[#219](https://github.com/openkruise/rollouts/pull/219),[#222](https://github.com/openkruise/rollouts/pull/222),[@myname4423](https://github.com/myname4423))

### Other Features:
- Enhanced flexibility by allowing free navigation between different steps within a rollout ([#218](https://github.com/openkruise/rollouts/pull/218),[@myname4423](https://github.com/myname4423))
- Added support for HTTPQueryParamMatch and HTTPPathMatch of the Gateway API in traffic management ([#204](https://github.com/openkruise/rollouts/pull/204),[@lujiajing1126](https://github.com/lujiajing1126))
- Integrated RequestHeaderModifier into LuaData, facilitating its use with custom network references like Istio ([#223](https://github.com/openkruise/rollouts/pull/223),[@lujiajing1126](https://github.com/lujiajing1126))
- Introduced a composite provider to support multiple network providers ([#224](https://github.com/openkruise/rollouts/pull/224),[@lujiajing1126](https://github.com/lujiajing1126))
- Got the canary service selector patched from pod template metadata ([#243](https://github.com/openkruise/rollouts/pull/243),[@lujiajing1126](https://github.com/lujiajing1126))
- Patched label `rollout-batch-id` to pods even without `rollout-id` in the workload ([#248](https://github.com/openkruise/rollouts/pull/248),[@PersistentJZH](https://github.com/PersistentJZH))
- Upgraded Gateway API version from 0.5.1 to 0.7.1 and Kubernetes version from 1.24 to 1.26 ([#237](https://github.com/openkruise/rollouts/pull/237),[@AiRanthem](https://github.com/AiRanthem))
- Enabled the option to skip canary service generation when using `trafficRoutings.customNetworkRefs` ([#200](https://github.com/openkruise/rollouts/pull/200),[@myname4423](https://github.com/myname4423))

### Bugfix:
- Filtered out ReplicaSets not part of the current Deployment to prevent frequent scaling issues when multiple deployments share the same selectors ([#191](https://github.com/openkruise/rollouts/pull/191),[@zhengjr9](https://github.com/zhengjr9))
- Synced the observed `rollout-id` to the BatchRelease CR status ([#193](https://github.com/openkruise/rollouts/pull/193),[@veophi](https://github.com/veophi))
- Checked deployment strategy during finalization to prevent random stuck states when used with KubeVela ([#198](https://github.com/openkruise/rollouts/pull/198),[@phantomnat](https://github.com/phantomnat))
- Resolved a Lua encoding structural error ([#209](https://github.com/openkruise/rollouts/pull/209),[@ls-2018](https://github.com/ls-2018))
- Corrected batch ID labeling in partition-style releases when pod recreation happens ([#246](https://github.com/openkruise/rollouts/pull/246),[@myname4423](https://github.com/myname4423))

### Breaking Changes:
- Restricted the ability to set traffic percentage or match selectors in a partition-style release step when exceeding 30% replicas. Use the `rollouts.kruise.io/partition-replicas-limit` annotation to override this default threshold. Setting the threshold to 100% restores the previous behavior ([#225](https://github.com/openkruise/rollouts/pull/225),[@myname4423](https://github.com/myname4423))

## v0.5.0
### Resources Graduating to BETA

After more than a year of development, we have now decided to upgrade the following resources to v1beta1, as follows:
- Rollout
- BatchRelease

Please refer to the [community documentation](https://openkruise.io/rollouts/user-manuals/api-specifications) for detailed api definitions.

**Note:** The v1alpha1 api is still available, and you can still use the v1alpha1 api in v0.5.0.
But we still recommend that you migrate to v1beta1 gradually, as some of the new features will only be available in v1beta1,
e.g., [Extensible Traffic Routing Based on Lua Script](https://openkruise.io/rollouts/developer-manuals/custom-network-provider/).

### Bump To V1beta1 Gateway API
Support for GatewayAPI from v1alpha2 to v1beta1, you can use v1beta1 gateway API.

### Extensible Traffic Routing Based on Lua Script

The Gateway API is a standard gateway resource given by the K8S community, but there are still a large number of users in the community who are still using some customized gateway resources, such as VirtualService, Apisix, and so on.
In order to adapt to this behavior and meet the diverse demands of the community for gateway resources, we support a traffic routing scheme based on Lua scripts.

Kruise Rollout utilizes a Lua-script-based customization approach for API Gateway resources (Istio VirtualService, Apisix ApisixRoute, Kuma TrafficRoute and etc.).
Kruise Rollout involves invoking Lua scripts to retrieve and update the desired configurations of resources based on release strategies and the original configurations of API Gateway resources (including spec, labels, and annotations).
It enables users to easily adapt and integrate various types of API Gateway resources without modifying existing code and configurations.

By using Kruise Rollout, users can:
- Customize Lua scripts for handling API Gateway resources, allowing for flexible implementation of resource processing and providing support for a wider range of resources.
- Utilize a common Rollout configuration template to configure different resources, reducing configuration complexity and facilitating user configuration.

### Traffic Routing with Istio
Based on the lua script approach, now we add built-in support for Istio resources VirtualService,
you can directly use Kruise Rollout to achieve Istio scenarios Canary, A/B Testing release.

### Others
- Bug fix: wait grace period seconds after pod creation/upgrade. ([#185](https://github.com/openkruise/rollouts/pull/185), [@veophi](https://github.com/veophi))

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
