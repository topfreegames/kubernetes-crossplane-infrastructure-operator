#!/bin/bash

if [[ $(kind get clusters | grep "${1}" -q) -eq 1 ]]; then
  kind create cluster --name "${1}" --image=kindest/node:v1.21.10@sha256:84709f09756ba4f863769bdcabe5edafc2ada72d3c8c44d6515fc581b66b029c
else
  kind delete cluster --name "${1}"
  kind create cluster --name "${1}" --image=kindest/node:v1.21.10@sha256:84709f09756ba4f863769bdcabe5edafc2ada72d3c8c44d6515fc581b66b029c
fi
