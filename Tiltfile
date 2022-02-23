load('ext://restart_process', 'docker_build_with_restart')

local_resource('Install Crossplane dependencies',
               'make apply-crossplane-dependencies'
)

local_resource('Wait Crossplane dependencies resources',
               'make wait-crossplane-dependencies-resources',
               resource_deps=[
                 'Install Crossplane dependencies'
               ]
)

local_resource('Install Cluster API CRDs',
               'make apply-capi-crds'
)

local_resource('Install Kubernetes Kops Operator CRDs',
               'make apply-kubernetes-kops-operator-crds'
)

local_resource('Install CRDs',
               'make install',
)

local_resource('Build manager binary',
               'make build',
)

docker_build_with_restart('manager:test',
             '.',
             dockerfile='./Dockerfile.dev',
             entrypoint='/manager',
             live_update=[
               sync('./bin/manager', '/manager')
             ],
             only=[
               "./bin/manager",
             ],
)

k8s_yaml('.kubernetes/dev/manifest.yaml')

k8s_resource(
  objects=[
    'provider-crossplane-system:namespace',
    'provider-crossplane-controller-manager:serviceaccount',
    'provider-crossplane-leader-election-role:role',
    'provider-crossplane-manager-role:clusterrole',
    'provider-crossplane-metrics-reader:clusterrole',
    'provider-crossplane-proxy-role:clusterrole',
    'provider-crossplane-leader-election-rolebinding:rolebinding',
    'provider-crossplane-manager-rolebinding:clusterrolebinding',
    'provider-crossplane-proxy-rolebinding:clusterrolebinding',
    'provider-crossplane-manager-config:configmap'
  ],
  new_name='Deploy Kubernetes resources'
)
