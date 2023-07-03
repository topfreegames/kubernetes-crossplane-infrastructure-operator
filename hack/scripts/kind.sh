#!/bin/bash

set -e

if [[ $(kind get clusters | grep "${1}" -q) -eq 1 ]]; then
  kind create cluster --name "${1}" --image="${2}"
else
  kind delete cluster --name "${1}"
  kind create cluster --name "${1}" --image="${2}"
fi
