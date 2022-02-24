#!/bin/bash
kubectl apply -f ./hack/assets/dependencies/crossplane.yaml

while ! kubectl get crd | grep -q "providers.pkg.crossplane.io" > /dev/null; do
    sleep 2
done 
kubectl apply -f ./hack/assets/dependencies/provider.yaml
sleep 5