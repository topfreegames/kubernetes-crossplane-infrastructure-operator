#!/bin/bash

if [[ $(kind get clusters | grep "${1}" -q) -eq 1 ]]; then
  kind create cluster --name "${1}" --image kindest/node:v1.22.7
else
  kind delete cluster --name "${1}"
  kind create cluster --name "${1}" --image kindest/node:v1.22.7
fi
