load('ext://restart_process', 'docker_build_with_restart')

local_resource('Wait AWS Credentials',
  'make wait-aws-credentials',
)

local_resource('Install Crossplane',
  'make apply-crossplane',
)

local_resource('Install Cluster API',
  'make apply-capi',
)

local_resource('Install Kubernetes Kops Operator',
  'make apply-kops-operator',
)

local_resource('Install CRDs',
  'make apply-crds',
)

local_resource('Build manager binary',
  'make build',
)

docker_build_with_restart('tfgco/kubernetes-crossplane-infrastructure-operator',
  '.',
  dockerfile = './Dockerfile.dev',
  entrypoint = '/manager',
  live_update = [
    sync('./bin/manager', '/manager')
  ],
  only = [
    "./bin/manager",
  ],
)

k8s_yaml('.kubernetes/manifests.yaml')

k8s_resource(
  objects = [
    'kubernetes-crossplane-infrastructure-operator-system:namespace',
    'kubernetes-crossplane-infrastructure-operator-controller-manager:serviceaccount',
    'kubernetes-crossplane-infrastructure-operator-leader-election-role:role',
    'kubernetes-crossplane-infrastructure-operator-manager-role:clusterrole',
    'kubernetes-crossplane-infrastructure-operator-leader-election-rolebinding:rolebinding',
    'kubernetes-crossplane-infrastructure-operator-manager-rolebinding:clusterrolebinding',
    'kubernetes-crossplane-infrastructure-operator-manager-config:configmap',
    'clustermeshes.clustermesh.infrastructure.wildlife.io:customresourcedefinition',
    'securitygroups.ec2.aws.wildlife.io:customresourcedefinition',
  ],
  new_name = 'Deploy Kubernetes resources'
)