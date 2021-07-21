#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail
set -x

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
# go get -u k8s.io/code-generator@v0.20.8
CODEGEN_PKG=${CODEGEN_PKG:-${GOPATH}/pkg/mod/k8s.io/code-generator@v0.20.8}

# generate the code with:
# --output-base    because this script should also be able to run inside the vendor dir of
#                  k8s.io/kubernetes. The output-base is needed for the generators to output into the vendor dir
#                  instead of the $GOPATH directly. For normal projects this can be dropped.
bash -x "${CODEGEN_PKG}"/generate-groups.sh "all" \
  github.com/ysicing/cr/pkg/client github.com/ysicing/cr/apis \
  tools:v1beta1 \
  --go-header-file "${SCRIPT_ROOT}"/hack/boilerplate.go.txt

# To use your own boilerplate text append:
#   --go-header-file "${SCRIPT_ROOT}"/hack/custom-boilerplate.go.txt