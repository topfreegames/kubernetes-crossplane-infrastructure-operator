#!/bin/bash

kubectl apply -f ./hack/assets/dependencies/cert-manager.yaml --context kind-$1

while ! kubectl get crd --context kind-$1 | grep -q "certificates.cert-manager.io" 2> /dev/null; do
    sleep 2
done 

while ! kubectl get pods -n cert-manager | grep webhook | grep -q "1/1" 2> /dev/null; do
    sleep 2
done 

kubectl apply -f ./hack/assets/dependencies/capi.yaml -n capi-system --context kind-$1

while ! kubectl get crd --context kind-$1 | grep -q "clusters.cluster.x-k8s.io" 2> /dev/null; do
    sleep 2
done 
