# kubernetes-crossplane-infrastructure-operator

## What is Kubernetes Crossplane Infrastructure Operator?
Kubernetes Crossplane Infrastructure Operator is a operator designed initially to complement [Cluster API](https://github.com/kubernetes-sigs/cluster-api) providers, by providing a way to manage infrastructure resources in AWS leveraging on Crossplane. Today it only supports [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) provider and will support to other cluster-api providers in the future.
See this [document](docs/README.md) for more details.

## Features
- Manages Security Groups using as reference [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) custom resources.
- Manages Clustermesh between Kubernetes clusters being managed by [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator)


