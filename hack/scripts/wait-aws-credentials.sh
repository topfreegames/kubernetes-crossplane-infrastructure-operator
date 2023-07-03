#!/bin/bash

kubectl create namespace provider-crossplane-system --context kind-$1

kubectl create namespace kubernetes-kops-operator-system --context kind-$1

while ! kubectl get secret -n kubernetes-kops-operator-system --context kind-$1 | grep -q aws-credentials 2> /dev/null; do
    echo "Setup the AWS credentials in the kubernetes-kops-operator-system namespace"
    sleep 2
done 

while ! kubectl get secret -n provider-crossplane-system --context kind-$1 | grep -q aws-credentials 2> /dev/null; do
    echo "Setup the AWS credentials in the provider-crossplane-system namespace"
    sleep 2
done 