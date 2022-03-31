# Rollouts
[![License](https://img.shields.io/badge/license-Apache%202-4EB1BA.svg)](https://www.apache.org/licenses/LICENSE-2.0.html)

## Introduction
Kruise Rollouts is **a Bypass component which provides advanced deployment capabilities such as canary, traffic routing, and progressive delivery features, for a series of Kubernetes workloads, such as Deployment and CloneSet.**

## Why Kruise Rollouts?
- **Functionality**：
    - Support multi-batch delivery for Deployment/CloneSet.
    - Support Nginx/ALB/Istio traffic routing control during rollout.

- **Flexibility**:
    - Support scaling up/down to workloads during rollout.
    - Can be applied to newly-created or existing workload objects directly;
    - Can be ridden out of at any time when you needn't it without consideration of unavailable workloads and traffic problems.
    - Can cooperate with other native/third-part Kubernetes controllers/operators, such as HPA and WorkloadSpread.

- **Non-Invasion**:
    - Does not invade native workload controllers.
    - Does not replace user-defined workload and traffic configurations.

- **Extensibility**:
    - Easily extend to other traffic routing types, or workload types via plugin codes.

- **Easy-integration**:
    - Easily integrate with classic or GitOps-style Kubernetes-based PaaS.

## Quick Start
- [Getting Started](docs/getting_started/introduction.md)

## Community
Active communication channels:

- Slack: [OpenKruise channel](https://kubernetes.slack.com/channels/openkruise) (*English*)
- DingTalk：Search GroupID `23330762` (*Chinese*)
- WeChat: Search User `openkruise` and let the robot invite you (*Chinese*)
- Bi-weekly Community Meeting (APAC, *Chinese*):
  - Thursday 19:00 GMT+8 (Asia/Shanghai), [Calendar](https://calendar.google.com/calendar/u/2?cid=MjdtbDZucXA2bjVpNTFyYTNpazV2dW8ybHNAZ3JvdXAuY2FsZW5kYXIuZ29vZ2xlLmNvbQ)
  - [Meeting Link(zoom)](https://us02web.zoom.us/j/87059136652?pwd=NlI4UThFWXVRZkxIU0dtR1NINncrQT09)
  - [Notes and agenda](https://shimo.im/docs/gXqmeQOYBehZ4vqo)
- Bi-weekly Community Meeting (*English*): TODO

## Acknowledge
- The global idea is from both OpenKruise and KubeVela communities, and the basic code of rollout is inherited from the KubeVela Rollout.
- This project is maintained by both contributors from [OpenKruise](https://openkruise.io/) and [KubeVela](https://kubevela.io).

## License
Kruise Rollout is licensed under the Apache License, Version 2.0. See [LICENSE](./LICENSE.md) for the full license text.

