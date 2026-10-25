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
    'kcio-system:namespace',
    'kcio-controller-manager:serviceaccount',
    'kcio-leader-election-role:role',
    'kcio-manager-role:clusterrole',
    'kcio-leader-election-rolebinding:rolebinding',
    'kcio-manager-rolebinding:clusterrolebinding',
    'kcio-manager-config:configmap',
  ],
  new_name = 'Deploy Kubernetes resources'
)