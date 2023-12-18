# provider-crossplane

# Overview
The purpose of this repository is to manage and integrate some infrastructure resources in AWS leveraging on [Crossplane](https://github.com/crossplane/crossplane). It's currently in an early development stage. 

# Running

Before you start make sure you have access to the AWS account where you'll be creating your resources for testing. Also on your machine you should have Docker and tilt properly configured, including [enable kubernetes on docker](https://docs.docker.com/desktop/kubernetes/). Next we can start running

```
make setup-dev-env
```

The script will start up tilt and by the end you'll be able to open tilt UI by pressing SPACE. On that UI you'll see 8 resources on your control cluster stuck on the `Waiting on Wait AWS Credentials` step. 

Now open a new shell and set the environment vars for the AWS credentials of the account you'll use, next run the following script to create the necessary kubernetes secrets with those credentials in your local tilt cluster.

```
./hack/scripts/aws-provider-config.sh
```
If the script is successful you should see that all the components on tilt UI are running after a while.

Now run the following script to create your testing resources

```
KOPS_DOMAIN=your.k8s.domain KOPS_BUCKET=kops-config-bucket KOPS_ZONE=aws-zone hack/scripts/apply-cluster-crs.sh kaas-cluster
```