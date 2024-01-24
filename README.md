# provider-crossplane

## What is Provider Crossplane?
Provider Crossplane is a operator designed to complement the [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) by providing a way to manage infrastructure resources in AWS leveraging on [Crossplane](https://github.com/crossplane/crossplane).

See this [document](docs/README.md) for more details.

## Features
- Manages Security Groups using as reference [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) custom resources.
- Manages mesh between Kubernetes clusters being managed by [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator)


