# kubernetes-crossplane-infrastructure-operator

## What is Kubernetes Crossplane Infrastructure Operator?
Kubernetes Crossplane Infrastructure Operator is a operator designed initially to complement [Cluster API](https://github.com/kubernetes-sigs/cluster-api) providers, by providing a way to manage infrastructure resources in AWS leveraging on Crossplane. Today it only supports [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) provider and will support to other cluster-api providers in the future.
See this [document](docs/README.md) for more details.

## Features
- Manages Security Groups using as reference [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator) custom resources.
- Manages Clustermesh between Kubernetes clusters being managed by [Kops Operator](https://github.com/topfreegames/kubernetes-kops-operator)



## Worker count
Each controller reconciles a number of objects at the same time. Set the number with these flags. The value must be 1 or more.

- `--securitygroup-worker-count` (default `5`): the number of SecurityGroups. Two SecurityGroups of the same cluster are not reconciled at the same time, because they change the same launch templates, Auto Scaling groups, instances, Ocean launch specs, KopsMachinePools and KopsControlPlane. SecurityGroups of different clusters are reconciled at the same time.
- `--clustermesh-worker-count` (default `1`): the number of Clusters for the ClusterMesh controller. All clusters of a mesh write the same ClusterMesh, and two clusters can both create the peering between them. Keep the value at 1.

Each worker makes its AWS and Spot calls one after the other, so more workers send more calls at the same time. The AWS calls of a cluster go to the account and region of that cluster, and clusters in the same account and region share its API rate limits. All Spot calls go to the one Spot account of the operator.

## Reconcile one cluster
Start the manager with `--cluster=<name>` to reconcile only the objects of one cluster. The name is the name of the cluster's KopsControlPlane. This works with and without `--dry-run`: without `--dry-run`, the manager makes real changes to that cluster. Do not run it next to the deployed operator without `--dry-run`; both would write the same objects. The manager then reconciles:

- the Cluster whose control plane is that KopsControlPlane. Its reconcile can also change objects of the other clusters in its mesh, for example the peerings with them.
- the SecurityGroups whose `infrastructureRef`, or last applied `infrastructureRef`, points to that KopsControlPlane, to a KopsMachinePool of that cluster, or to a KopsMachinePool that does not exist anymore. A SecurityGroup that also references other clusters is reconciled in full, so it also changes those clusters.

The filter does not check namespaces. Two KopsControlPlanes with the same name in different namespaces both match. The manager still watches the objects of all clusters; it only skips their reconciles.

## Dry-run mode
Start the manager with `--dry-run` to see what a version would change before you roll it out. The manager reconciles as usual, but it makes no changes:

- Kubernetes writes (create, update, patch, delete, status and other subresources) go to the API server with `dryRun=All`. The API server validates them and runs admission, but does not store them.
- AWS and Spot writes are not sent. The manager logs them and returns a fake success result: `CreateLaunchTemplateVersion`, `ModifyInstanceAttribute`, `UpdateAutoScalingGroup` and Ocean `UpdateLaunchSpec`. AWS and Spot reads are real.
- Events are logged, not created.

This applies to the Kubernetes client, the event recorder and the EC2, Auto Scaling and Ocean client factories of the reconcilers. A client that a future version creates in another way is not covered.

Each create, update, patch and delete the manager would make is logged with the message `would write`, the verb, the kind and the name, before the request is sent. If the API server rejects the request, the reconcile error follows in the log. `DeleteAllOf` and subresource creates are sent with `dryRun=All` but are not logged; this operator does not use them.

Creates also log the object. Updates also log a JSON merge patch from the object on the API server to the object that is sent. This patch can contain fields that the API server ignores, for example status changes on an update of the main resource. Patches log the patch that is sent; for a merge patch made from the full object, that is the full object. The payload of a Secret is never logged. Events are logged with the message `would record event`.

### Run next to the real operator
`--dry-run` turns off leader election, so the dry-run manager does not take the lease from the real manager. It also turns off the metrics server, so the dry-run manager does not report metrics with the same names and labels as the real manager. Both managers reconcile the same objects at the same time.

Run it locally with a kubeconfig for the management cluster. Use another port for the health probes if 8081 is in use:

```sh
go run ./cmd/main.go --dry-run --health-probe-bind-address=:18081
```

Or deploy a second Deployment with the new image, a different name and the same service account. Give it pod labels and a selector that are different from the real manager, so that no Service of the real manager selects the dry-run pod. Remove the `ad.datadoghq.com/*` pod annotations, because the dry-run pod has no metrics endpoint. Give its logs a different service tag, or exclude the pod from log monitors: the dry-run manager logs errors that are expected in dry-run mode. Replace `--leader-elect` with `--dry-run` in the container args:

```yaml
        args:
        - --dry-run
```

Add `--cluster=<name>` to reconcile only one cluster, see [Reconcile one cluster](#reconcile-one-cluster).

The dry-run manager needs the same RBAC, AWS credentials and Spot credentials as the real manager. The API server authorizes a dry-run request the same way as a real request.

### Plan the changes once
Add `--once` to `--dry-run` to reconcile every object once, print the changes and exit, like `terraform plan`. Use it to see what a new version would change before you deploy it:

```sh
go run ./cmd/main.go --dry-run --once > plan.txt
```

Only the cache of the manager starts: no controllers, leader election, metrics or health probes. The plan reconciles every Cluster for the ClusterMesh controller, then every SecurityGroup, with `--clustermesh-worker-count` and `--securitygroup-worker-count` workers. With `--cluster`, it reconciles only the objects of that cluster and fails when no Cluster matches the name. Logs go to stderr and the plan to stdout, grouped by cluster:

- Kubernetes objects: `+` create, `~` update and `-` delete, with a unified diff of the object in YAML, like `kubectl diff`. The plan compares the object that the API server returns for the dry-run write with the object in the cluster, and leaves out writes that change nothing. It ignores `resourceVersion`, `generation`, `uid`, `creationTimestamp` and `managedFields`, and shows lists with their elements sorted. An update that only changes the order of list elements is labeled `order only`. An update that removes the last finalizer of an object that is being deleted is a delete, because the API server then deletes the object. If the reconciles of several objects, for example the Clusters of a mesh, would write different versions of the same object, the plan shows each version. Secret values show as a hash.
- AWS and Spot writes: the security groups of an instance and of a launch template version before and after, the new launch template version of an Auto Scaling group and the new security groups of an Ocean launch spec.
- The reconciles that did not complete: `pending` when the real controller would retry soon, for example because a Crossplane resource is not ready or the reconcile needs an object that this plan creates, and `error` otherwise. A write that the API server rejects is an error even when the reconciler ignores it. A reconcile that hits a conflict, most likely because the running operator just wrote the object, runs once more.

The exit code is `0` when there are no changes, `2` when there are changes, and `1` when a reconcile fails or is pending, or `--timeout` (default `15m`) ends first. The plan is printed in all cases and ends with a `Plan:` line. A Go panic also exits with `2`, so a CI job should treat `2` as changes only when that line is there.

A plan shows one step, because nothing is stored. What the operator then does with an object that the plan creates or changes, for example the Crossplane SecurityGroup of a SecurityGroup whose ingress rules change, shows in the plan after the deploy. Two writes to the same launch template show as two versions that each add one security group. Paused SecurityGroups are skipped. A cluster that fails in the running operator makes every plan exit with `1`.

The plan needs list and watch on the kinds that the operator reads, including the Secrets of all namespaces, which hold the AWS credentials of the clusters; a forbidden list or watch makes it wait until `--timeout`. It needs the Spot credentials when a KopsMachinePool uses Spot.

The image is `gcr.io/distroless/static:debug-nonroot`, which has a busybox shell, so a CI job can use it as the job image, with the entrypoint set to `[""]`, and run `/manager --dry-run --once`.

### Limits
- Nothing is stored, so the same actions repeat on every reconcile. For example, the manager logs the same finalizer patch each time, and a delete does not remove the object.
- The dry-run manager uses the same AWS and Spot credentials as the real manager, and because nothing is stored it takes the longest path on every reconcile. The extra read calls count against the same EC2 and Spot API rate limits. Use `--cluster` in production to keep this load small. The dry-run manager also adds load on the API server: it opens its own watches on all clusters, even with `--cluster`, every update makes an extra read, and every dry-run write goes through the admission webhooks.
- Write results are fake. A created launch template version gets the latest version number plus one, and the next steps use that number. A created Kubernetes object does not exist when the next reconcile reads it, so later steps can fail or take a different path than in a real run.
- Admission webhooks run on dry-run requests as on real requests. If a validating webhook or a policy engine rejects a write, the dry-run manager logs a reconcile error. The real write would fail the same way.
- A webhook with `sideEffects: NoneOnDryRun` must check the dry-run flag itself. If it does not, it can make real changes during a dry run. Before you run in production, check the webhooks that match the kinds this operator writes.
