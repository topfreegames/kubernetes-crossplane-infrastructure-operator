#!/bin/bash

if ! kind get clusters &> /dev/stdout | grep -q "${1}"; then
  kind create cluster --name "${1}" --image="${2}"
else
  echo "Kind cluster already created"
fi
