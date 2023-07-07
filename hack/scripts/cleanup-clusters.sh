#!/bin/bash
kubectl get cl -A --no-headers --context kind-$1 | while read line; do 
    namespace=$(echo ${line} | awk '{print $1}')
    name=$(echo ${line} | awk '{print $2}')
    bucket=$(kubectl get kcp ${name} -n ${namespace} --context kind-$1 -o yaml | yq .spec.kopsClusterSpec.configBase | cut -d/ -f3)
    echo "Deleting cluster ${name} in ${namespace}"
    kubectl delete namespace ${namespace} --context kind-$1 --wait=false
	kops delete cluster --state s3://${bucket} --name ${name} --yes
done

