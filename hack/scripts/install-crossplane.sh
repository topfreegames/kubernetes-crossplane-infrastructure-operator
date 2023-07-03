#!/bin/bash

kubectl apply -f ./hack/assets/dependencies/crossplane.yaml -n provider-crossplane-system --context kind-$1

while ! kubectl get crd --context kind-$1 | grep -q "providers.pkg.crossplane.io" 2> /dev/null; do
    sleep 2
done 
kubectl apply -f ./hack/assets/dependencies/provider.yaml -n provider-crossplane-system --context kind-$1

while ! kubectl get crd --context kind-$1 | grep -q "providerconfigs.aws.crossplane.io" 2> /dev/null; do
    sleep 2
done 
kubectl apply -f ./hack/assets/dependencies/providerconfig.yaml -n provider-crossplane-system --context kind-$1

while ! kubectl rollout status deployment -n provider-crossplane-system crossplane --context kind-$1 2> /dev/null; do
    sleep 2
done 

while ! kubectl rollout status -n provider-crossplane-system  --context kind-$1 $(kubectl get deploy -n provider-crossplane-system -o name --context kind-$1 | grep provider-aws) 2> /dev/null; do
    sleep 2
done 


