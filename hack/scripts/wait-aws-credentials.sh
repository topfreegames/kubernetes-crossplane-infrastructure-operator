#!/bin/bash

kubectl create namespace kubernetes-crossplane-infrastructure-operator-system --context kind-$1

kubectl create namespace kubernetes-kops-operator-system --context kind-$1

while ! kubectl get secret -n kubernetes-kops-operator-system --context kind-$1 | grep -q aws-credentials 2> /dev/null; do
    echo "Setup the AWS credentials in the kubernetes-kops-operator-system namespace"
    sleep 2
done 

while ! kubectl get secret -n kubernetes-crossplane-infrastructure-operator-system --context kind-$1 | grep -q aws-credentials 2> /dev/null; do
    echo "Setup the AWS credentials in the kubernetes-crossplane-infrastructure-operator-system namespace"
    sleep 2
done 