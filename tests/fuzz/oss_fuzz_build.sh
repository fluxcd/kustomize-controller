#!/usr/bin/env bash

# Copyright 2022 The Flux authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euxo pipefail

GOPATH="${GOPATH:-/root/go}"
GO_SRC="${GOPATH}/src"
PROJECT_PATH="github.com/fluxcd/kustomize-controller"

cd "${GO_SRC}"

# Move fuzzer to their respective directories.
# This removes dependency noises from the modules' go.mod and go.sum files.
mv "${PROJECT_PATH}/tests/fuzz/age_fuzzer.go" "${PROJECT_PATH}/internal/sops/age/"
mv "${PROJECT_PATH}/tests/fuzz/pgp_fuzzer.go" "${PROJECT_PATH}/internal/sops/pgp/"

# Some private functions within suite_test.go are extremly useful for testing.
# Instead of duplicating them here, or refactoring them away, this simply renames
# the file to make it available to "non-testing code".
# This is a temporary fix, which will cease once the implementation is migrated to
# the built-in fuzz support in golang 1.18.
cp "${PROJECT_PATH}/controllers/suite_test.go" "${PROJECT_PATH}/tests/fuzz/fuzzer_helper.go"
sed -i 's;KustomizationReconciler;abc.KustomizationReconciler;g' "${PROJECT_PATH}/tests/fuzz/fuzzer_helper.go"
sed -i 's;import (;import(\n	abc "github.com/fluxcd/kustomize-controller/controllers";g' "${PROJECT_PATH}/tests/fuzz/fuzzer_helper.go"

pushd "${PROJECT_PATH}"

go get -d github.com/AdaLogics/go-fuzz-headers

compile_go_fuzzer "${PROJECT_PATH}/internal/sops/age/" FuzzAge fuzz_age
compile_go_fuzzer "${PROJECT_PATH}/internal/sops/pgp/" FuzzPgp fuzz_pgp

popd

pushd "${PROJECT_PATH}/tests/fuzz"

# Setup files to be embedded into controllers_fuzzer.go's testFiles variable.
mkdir -p testdata/crd
mkdir -p testdata/sops
cp ../../config/crd/bases/*.yaml testdata/crd
cp ../../controllers/testdata/sops/age.txt testdata/sops
cp ../../controllers/testdata/sops/pgp.asc testdata/sops

# Use main go.mod in order to conserve the same version across all dependencies.
cp ../../go.mod .
cp ../../go.sum .

sed -i 's;module .*;module github.com/fluxcd/kustomize-controller/tests/fuzz;g' go.mod
sed -i 's;api => ./api;api => ../../api;g' go.mod
echo "replace github.com/fluxcd/kustomize-controller => ../../" >> go.mod

go mod download

compile_go_fuzzer "${PROJECT_PATH}/tests/fuzz/" FuzzControllers fuzz_controllers

popd
