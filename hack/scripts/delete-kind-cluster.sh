#!/bin/bash

set -e
docker rm -f -v kaas-cluster-control-plane
kind delete cluster --name "${1}"