# kubernetes-crossplane-infrastructure-operator

## What is Kubernetes Crossplane Infrastructure Operator?
Kubernetes Crossplane Infrastructure Operator is a operator designed initially to complement [Cluster API](https://github.com/kubernetes-sigs/cluster-api) providers, by providing a way to manage infrastructure resources in AWS leveraging on Crossplane. Today it only supports [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) provider and will support to other cluster-api providers in the future.
See this [document](docs/README.md) for more details.

## Features
- Manages Security Groups using as reference [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) custom resources.
- Manages Clustermesh between Kubernetes clusters being managed by [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator)



## Dry-run mode
Start the manager with `--dry-run` to see what a version would change before you roll it out. The manager reconciles as usual, but it makes no changes:

- Kubernetes writes (create, update, patch, delete, status and other subresources) go to the API server with `dryRun=All`. The API server validates them and runs admission, but does not store them.
- AWS and Spot writes are not sent. The manager logs them and returns a fake success result: `CreateLaunchTemplateVersion`, `ModifyInstanceAttribute`, `UpdateAutoScalingGroup` and Ocean `UpdateLaunchSpec`. AWS and Spot reads are real.
- Events are logged, not created.

Each write the manager would make is logged with the message `would write`, the verb, the kind and the name. Patches and updates also log the change as a JSON patch. Events are logged with the message `would record event`.

### Run next to the real operator
`--dry-run` turns off leader election, so the dry-run manager does not take the lease from the real manager. Both managers reconcile the same objects at the same time.

Run it locally with a kubeconfig for the management cluster. Use other ports for metrics and probes if they are in use:

```sh
go run ./cmd/main.go --dry-run --metrics-bind-address=:0 --health-probe-bind-address=:18081
```

Or deploy a second Deployment with the new image, a different name and the same service account. Replace `--leader-elect` with `--dry-run` in the container args:

```yaml
        args:
        - --dry-run
```

The dry-run manager needs the same RBAC, AWS credentials and Spot credentials as the real manager. The API server authorizes a dry-run request the same way as a real request.

### Limits
- Nothing is stored, so the same actions repeat on every reconcile. For example, the manager logs the same finalizer patch each time, and a delete does not remove the object.
- Write results are fake. A created launch template version gets the latest version number plus one, and the next steps use that number. A created Kubernetes object does not exist when the next reconcile reads it, so later steps can fail or take a different path than in a real run.
- Admission webhooks that do not declare `sideEffects: None` or `NoneOnDryRun` reject dry-run requests. These rejections show as reconcile errors in the dry-run manager only.
