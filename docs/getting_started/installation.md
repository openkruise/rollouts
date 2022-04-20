# Installation

## Requirements
- Install Kubernetes Cluster, requires **Kubernetes version >= 1.19**.
- (Optional, If Use CloneSet) Helm installation of OpenKruise, **Since v1.1.0**, Reference [Install OpenKruise](https://openkruise.io/docs/installation).

## Install with helm

Kruise Rollout can be simply installed by helm v3.1+, which is a simple command-line tool and you can get it from [here](https://github.com/helm/helm/releases).

```bash
# Firstly add openkruise charts repository if you haven't do this.
$ helm repo add openkruise https://openkruise.github.io/charts/

# [Optional]
$ helm repo update

# Install the latest version.
$ helm install kruise-rollout openkruise/kruise-rollout --version 0.1.0
```

## Uninstall

Note that this will lead to all resources created by Kruise Rollout, including webhook configurations, services, namespace, CRDs and CR instances Kruise Rollout controller, to be deleted!

Please do this ONLY when you fully understand the consequence.

To uninstall kruise rollout if it is installed with helm charts:

```bash
$ helm uninstall kruise-rollout
release "kruise-rollout" uninstalled
```

## What's Next
Here are some recommended next steps:
- Learn Kruise Rollout's [Basic Usage](../tutorials/basic_usage.md).
