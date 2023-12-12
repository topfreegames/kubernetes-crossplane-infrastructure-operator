#!/bin/bash

if [[ -z "${KOPS_DOMAIN}" ]]; then
  echo "Define which Route53 Domain is going to be used defining KOPS_DOMAIN as environment variable i.e.: dev.example.com"
  exit 1
fi

if [[ -z "${KOPS_BUCKET}" ]]; then
  echo "Define which S3 Bucket is going to be used defining KOPS_BUCKET as environment variable i.e.: kubernetes-test-bucket"
  exit 1
fi

if [[ -z "${KOPS_ZONE}" ]]; then
  echo "Define which zone is going to be used defining KOPS_ZONE as environment variable i.e.: us-east-1a"
  exit 1
fi

cat hack/assets/cr/test-cluster-a.yaml |  sed  "s/<ZONE>/${KOPS_ZONE}/g" | sed  "s/<DOMAIN>/${KOPS_DOMAIN}/g" | sed "s/<BUCKET>/${KOPS_BUCKET}/g" | kubectl apply -f - --context kind-$1
cat hack/assets/cr/test-cluster-b.yaml |  sed  "s/<ZONE>/${KOPS_ZONE}/g" | sed  "s/<DOMAIN>/${KOPS_DOMAIN}/g" | sed "s/<BUCKET>/${KOPS_BUCKET}/g" | kubectl apply -f - --context kind-$1
cat hack/assets/cr/test-cluster-c.yaml |  sed  "s/<ZONE>/${KOPS_ZONE}/g" | sed  "s/<DOMAIN>/${KOPS_DOMAIN}/g" | sed "s/<BUCKET>/${KOPS_BUCKET}/g" | kubectl apply -f - --context kind-$1