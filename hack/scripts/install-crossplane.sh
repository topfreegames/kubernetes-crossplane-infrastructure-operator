#!/bin/bash
set -e

wait_crd_established() {
    for _ in $(seq 60); do
        [ "$(kubectl get crd "$1" --context "$2" -o jsonpath='{.status.conditions[?(@.type=="Established")].status}' 2> /dev/null)" = "True" ] && return 0
        sleep 2
    done
    echo "CRD $1 was not Established after 120s" >&2
    return 1
}

kubectl apply -f ./hack/assets/dependencies/crossplane.yaml -n kubernetes-crossplane-infrastructure-operator-system --context kind-$1

wait_crd_established providers.pkg.crossplane.io kind-$1
kubectl apply -f ./hack/assets/dependencies/provider.yaml -n kubernetes-crossplane-infrastructure-operator-system --context kind-$1

wait_crd_established providerconfigs.aws.crossplane.io kind-$1
kubectl apply -f ./hack/assets/dependencies/providerconfig.yaml -n kubernetes-crossplane-infrastructure-operator-system --context kind-$1

while ! kubectl rollout status deployment -n kubernetes-crossplane-infrastructure-operator-system crossplane --context kind-$1 2> /dev/null; do
    sleep 2
done 

while ! kubectl rollout status -n kubernetes-crossplane-infrastructure-operator-system  --context kind-$1 $(kubectl get deploy -n kubernetes-crossplane-infrastructure-operator-system -o name --context kind-$1 | grep provider-aws) 2> /dev/null; do
    sleep 2
done 


