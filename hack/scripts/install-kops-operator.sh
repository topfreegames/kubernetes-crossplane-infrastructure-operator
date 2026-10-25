#!/bin/bash
set -e

kubectl apply --server-side -f ./hack/assets/dependencies/kubernetes-kops-operator.yaml -n kubernetes-kops-operator-system --context kind-$1

kubectl wait --for=condition=Established crd/kopscontrolplanes.controlplane.cluster.x-k8s.io crd/kopsmachinepools.infrastructure.cluster.x-k8s.io --timeout=120s --context kind-$1

