#!/usr/bin/env bash
set -eou pipefail

# get the script directory so we can build from the correct location
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")"; pwd)"

cd "${SCRIPT_DIR}/.."

echo "Check format ..."
gofmt -l "."

echo "Run Tests ..."
go test "./..." -coverpkg=github.com/garethjudson/batchy
