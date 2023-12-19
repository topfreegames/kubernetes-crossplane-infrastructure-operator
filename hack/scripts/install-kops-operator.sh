#!/bin/bash

kubectl apply -f ./hack/assets/dependencies/kubernetes-kops-operator.yaml -n kubernetes-kops-operator-system --context kind-$1

while ! kubectl rollout status -n kubernetes-kops-operator-system deploy kubernetes-kops-operator-controller-manager-dev --context kind-$1 2> /dev/null; do
    sleep 2
done 

