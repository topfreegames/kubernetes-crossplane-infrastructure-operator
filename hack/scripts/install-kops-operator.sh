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

kubectl apply --server-side -f ./hack/assets/dependencies/kubernetes-kops-operator.yaml -n kubernetes-kops-operator-system --context kind-$1

wait_crd_established kopscontrolplanes.controlplane.cluster.x-k8s.io kind-$1
wait_crd_established kopsmachinepools.infrastructure.cluster.x-k8s.io kind-$1

