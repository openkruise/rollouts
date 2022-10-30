---
title: RolloutHistory-Proposal

authors:
  - "@yike21"

reviewers:
  - "@zmberg"
  - "@hantmac"
  - "@veophi"

creation-date: 2022-04-24
last-updated: 2022-10-28
status: implementable

---

# RolloutHistory

- Record the information such as rollout status, pod names, pod ips, rollout strategy... when user do a rollout action.
- Record the information of workload, service, ingress which is related to rollout.  

## Table of Contents

A table of contents is helpful for quickly jumping to sections of a proposal and for highlighting
any additional information provided beyond the standard proposal template.
[Tools for generating](https://github.com/ekalinin/github-markdown-toc) a table of contents from markdown are available.

- [RolloutHistory](#RolloutHistory)
    - [Table of Contents](#table-of-contents)
    - [Motivation](#motivation)
    - [Proposal](#proposal)
        - [User Stories](#user-stories)
            - [Story 1](#story-1)
            - [Story 2](#story-2)
        - [Implementation Details/Notes/Constraints](#implementation-detailsnotesconstraints)
        - [Risks and Mitigations](#risks-and-mitigations)
    - [Implementation History](#implementation-history)

## Motivation 

From the design doc of rollouts, we know that the rollouts resource is bound to a deployment or cloneset, which is a one to one mode.  
When users finish some rollout actions, they can not find some information in the past few actions.  
And a history record of rollout is very useful for rollout tracing.   
So we should record the information such as pod names, pod ips, rollout strategy... when users do a rollout action.  

It is useful for some user scenarios, such as: 

1. Help users to know what happened in the past few rollout actions.  
2. Record the pods released when user does a rollout action.  
3. Record the information of workload, service, ingress/gateway related to rollout  

## Proposal  

Define a CRD named `RolloutHistory`, and implement a controller to record information. the Golang type is like : 

```go
// RolloutHistorySpec defines the desired state of RolloutHistory
type RolloutHistorySpec struct {
	// Rollout indicates information of the rollout related with rollouthistory
	Rollout RolloutInfo `json:"rollout,omitempty"`
	// Workload indicates information of the workload, such as cloneset, deployment, advanced statefulset
	Workload WorkloadInfo `json:"workload,omitempty"`
	// Service indicates information of the service related with workload
	Service ServiceInfo `json:"service,omitempty"`
	// TrafficRouting indicates information of traffic route related with workload
	TrafficRouting TrafficRoutingInfo `json:"trafficRouting,omitempty"`
}

type NameAndSpecData struct {
	// Name indicates the name of object ref, such as rollout name, workload name, ingress name, etc.
	Name string `json:"name"`
	// Data indecates the spec of object ref
	Data runtime.RawExtension `json:"data,omitempty"`
}

// RolloutInfo indicates information of the rollout related
type RolloutInfo struct {
	// RolloutID indicates the new rollout
	// if there is no new RolloutID this time, ignore it and not execute RolloutHistory
	RolloutID       string `json:"rolloutID"`
	NameAndSpecData `json:",inline"`
}

// ServiceInfo indicates information of the service related
type ServiceInfo struct {
	NameAndSpecData `json:",inline"`
}

// TrafficRoutingInfo indicates information of Gateway API or Ingress
type TrafficRoutingInfo struct {
	// IngressRef indicates information of ingress
	Ingress *IngressInfo `json:"ingress,omitempty"`
	// HTTPRouteRef indacates information of Gateway API
	HTTPRoute *HTTPRouteInfo `json:"httpRoute,omitempty"`
}

// IngressInfo indicates information of the ingress related
type IngressInfo struct {
	NameAndSpecData `json:",inline"`
}

// HTTPRouteInfo indicates information of gateway API
type HTTPRouteInfo struct {
	NameAndSpecData `json:",inline"`
}

// WorkloadInfo indicates information of the workload, such as cloneset, deployment, advanced statefulset
type WorkloadInfo struct {
	metav1.TypeMeta `json:",inline"`
	NameAndSpecData `json:",inline"`
}

// RolloutHistoryStatus defines the observed state of RolloutHistory
type RolloutHistoryStatus struct {
	// Phase indicates phase of RolloutHistory, such as "pending", "updated", "completed"
	Phase string `json:"phase,omitempty"`
	// CanarySteps indicates the pods released each step
	CanarySteps []CanaryStepInfo `json:"canarySteps,omitempty"`
}

// CanaryStepInfo indicates the pods for a revision
type CanaryStepInfo struct {
	// CanaryStepIndex indicates step this revision
	CanaryStepIndex int32 `json:"canaryStepIndex,omitempty"`
	// Pods indicates the pods information
	Pods []Pod `json:"pods,omitempty"`
}

// Pod indicates the information of a pod, including name, ip, node_name.
type Pod struct {
	// Name indicates the node name
	Name string `json:"name,omitempty"`
	// IP indicates the pod ip
	IP string `json:"ip,omitempty"`
	// Node indicates the node which pod is located at
	Node string `json:"node,omitempty"`
}

// Phase indicates rollouthistory status/phase
const (
	PhaseCompleted string = "completed"
)

// RolloutHistory is the Schema for the rollouthistories API
type RolloutHistory struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RolloutHistorySpec   `json:"spec,omitempty"`
	Status RolloutHistoryStatus `json:"status,omitempty"`
}

// RolloutHistoryList contains a list of RolloutHistory
type RolloutHistoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RolloutHistory `json:"items"`
}
```

1. When users start a new `rollout` process and the phase of `rollout` is `RolloutPhaseProgressing`, a `RolloutHistory` is generated to record information for this `rollout` if there is not other `RolloutHistory` for it. The phase of `RolloutHistory` will be empty.  
2. When a `rollout` is completed and its phase is `RolloutPhaseHealthy`, `RolloutHistory` begin to record `.spec` (such as workload infomation, service information) and `.status` (canary pods information). After that, the phase of `RolloutHistory` is `PhaseCompleted`.
3. When the `rollout` process has completed, the `.status.canarySteps` of `RolloutHistory` record information of released pods.  
4. When a RolloutHistory is generated, RolloutHistory controller labels it with `rolloutIDLabel` and `rolloutNameLabel` which indicate the related rollout's name and rolloutID.
5. At any time, there is at most one RolloutHistory for a rollout with a specified pair `{RolloutID + RolloutName}`. If user does a rollout again but not changed its `RolloutID`, and there is already a RolloutHistory for it. The new `RolloutHistory` won't be generated.

### User Stories 

#### Story 1 

Users want to know what happened in the past few rollout actions, for example, the strategy in that rollout(the rollout strategy has been changed since last release), 
the detail conditions and so on.  

Related [Issue](https://github.com/openkruise/rollouts/issues/10).  

#### Story 2  
Users want to know the information of deployment/cloneset, service, ingress, gateway for this rollout.  

### Requirements 

`RolloutHistory` relies on `Rollout` resources. But `RolloutHistory` shouldn't be deleted with Rollout.  
`Rollout` should have `.spec.rolloutID`. When user does a rollout, `RolloutHistory` will not be generated until its `.status.canaryStatus.observedRolloutID` is set.  

### Implementation Details/Notes/Constraints 

1. What's the RolloutHistory phase designed?  
`.status.phase` indicates phase of a RolloutHistory. If phase is empty, it means that this RolloutHistory is generated and waiting for rollout to be completed. If phase is `PhaseCompleted`, it means the RolloutHistory has completed and record information. 

2. What's the rollout step state in a rollout step?  
When a rollout processes, its step state `.status.canaryStatus.canaryStepState` can be  
`null(*)` -> `StepUpgrade` -> `StepTrafficRouting` -> `StepMetricsAnalysis` -> `StepPaused` -> `StepReady` -> `StepUpgrade`/`Completed`. 

3. When should RolloutHistory record the rollout step information?  
RolloutHistory should record this rollout information when the phase of rollout is `RolloutPhaseHealthy` which means that this rollout has completed and the pods have been generated. 

4. How is the RolloutHistory phase changed?  
* `""` --a-->  `PhaseCompleted`   
  
- `""`(empty) means that Rollout is progressing, and RolloutHistory will be generated. A RolloutHistory will be generated only if the rollout have `.spec.rolloutID`, `.status.canaryStatus.observedRolloutID` and there is no other RolloutHistory for this rollout.
- `completed` means that this rollout is completed, and RolloutHistory have a record of this rollout's information, including the information of service, ingress/httpRoute, workload, rollout related and pods released.   

- `event a` means that the phase of rollout have been `RolloutPhaseHealthy`   

### Risks and Mitigations

- Currently, we don't want to listwatch pods in kruise-rollout, so how to record the pod names will be a problem. We know that Workload cloneSet gets `spec.selector` which is a label query over pods that should match the replica count. What's more, rollout controller will label the canary pods with `RolloutIDLabel` and `RolloutBatchIDLabel`. 
- There are many kind of release type, such as `canary rollout`,`blue-green rollout`, `batch release` and so on, the status of RolloutHistory may not cover all the status information in release types.
- RolloutHistory record pods by labels, they depend on the `RolloutBatchIDLabel` and `RolloutIDLabel` labeled by rollout controller.

## Implementation History  

- [ ] 24/04/2022: Proposal submission  
- [ ] 03/08/2022: Proposal updated submission  
- [ ] 21/10/2022: add RolloutHistory API  
- [ ] 28/10/2022: add RolloutHistory controller  
