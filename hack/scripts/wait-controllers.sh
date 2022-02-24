#!/usr/bin/env bash

kubectl rollout status deployment -n crossplane-system crossplane
sleep 10
kubectl rollout status -n crossplane-system  $(kubectl get deploy -n crossplane-system -o name | grep provider-aws)
