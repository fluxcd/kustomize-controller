# Image URL to use all building/pushing image targets
IMG ?= fluxcd/kustomize-controller:latest
# Produce CRDs that work back to Kubernetes 1.16
CRD_OPTIONS ?= crd:crdVersions=v1
SOURCE_VER ?= $(shell go list -m all | grep github.com/fluxcd/source-controller/api | awk '{print $$2}')

# Use the same version of SOPS already referenced on go.mod
SOPS_VER := $(shell go list -m all | grep github.com/getsops/sops | awk '{print $$2}')

# Repository root based on Git metadata
REPOSITORY_ROOT := $(shell git rev-parse --show-toplevel)
BUILD_DIR := $(REPOSITORY_ROOT)/build

# FUZZ_TIME defines the max amount of time, in Go Duration,
# each fuzzer should run for.
FUZZ_TIME ?= 1m

# If gobin not set, create one on ./build and add to path.
ifeq (,$(shell go env GOBIN))
GOBIN=$(BUILD_DIR)/gobin
else
GOBIN=$(shell go env GOBIN)
endif
export PATH:=$(GOBIN):${PATH}

# Allows for defining additional Go test args, e.g. '-tags integration'.
GO_TEST_ARGS ?=

# Allows for defining additional Docker buildx arguments, e.g. '--push'.
BUILD_ARGS ?= --load
# Architectures to build images for.
BUILD_PLATFORMS ?= linux/amd64

# Architecture to use envtest with
ENVTEST_ARCH ?= amd64

# Paths to download the CRD dependencies at.
GITREPO_CRD ?= config/crd/bases/gitrepositories.yaml
BUCKET_CRD ?= config/crd/bases/buckets.yaml
OCIREPO_CRD ?= config/crd/bases/ocirepositories.yaml

# Keep a record of the version of the downloaded source CRDs. It is used to
# detect and download new CRDs when the SOURCE_VER changes.
SOURCE_CRD_VER=$(BUILD_DIR)/.src-crd-$(SOURCE_VER)

# API (doc) generation utilities
CONTROLLER_GEN_VERSION ?= v0.14.0
GEN_API_REF_DOCS_VERSION ?= e327d0730470cbd61b06300f81c5fcf91c23c113

all: manager

# Download the envtest binaries to testbin
ENVTEST_ASSETS_DIR=$(BUILD_DIR)/testbin
ENVTEST_KUBERNETES_VERSION?=latest
install-envtest: setup-envtest
	mkdir -p ${ENVTEST_ASSETS_DIR}
	$(ENVTEST) use $(ENVTEST_KUBERNETES_VERSION) --arch=$(ENVTEST_ARCH) --bin-dir=$(ENVTEST_ASSETS_DIR)

SOPS = $(GOBIN)/sops
$(SOPS): ## Download latest sops binary if none is found.
	$(call go-install-tool,$(SOPS),github.com/getsops/sops/v3/cmd/sops@$(SOPS_VER))

# Run controller tests
KUBEBUILDER_ASSETS?="$(shell $(ENVTEST) --arch=$(ENVTEST_ARCH) use -i $(ENVTEST_KUBERNETES_VERSION) --bin-dir=$(ENVTEST_ASSETS_DIR) -p path)"
test: tidy generate fmt vet manifests api-docs download-crd-deps install-envtest $(SOPS)
	KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) go test ./... $(GO_TEST_ARGS) -v -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build -o $(BUILD_DIR)/bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go --metrics-addr=:8089

# Delete previously downloaded CRDs and record the new version of the source
# CRDs.
$(SOURCE_CRD_VER):
	rm -f $(BUILD_DIR)/.src-crd*
	$(MAKE) cleanup-crd-deps
	if ! test -d "$(BUILD_DIR)"; then mkdir -p $(BUILD_DIR); fi
	touch $(SOURCE_CRD_VER)

$(GITREPO_CRD):
	curl -s https://raw.githubusercontent.com/fluxcd/source-controller/${SOURCE_VER}/config/crd/bases/source.toolkit.fluxcd.io_gitrepositories.yaml -o $(GITREPO_CRD)

$(BUCKET_CRD):
	curl -s https://raw.githubusercontent.com/fluxcd/source-controller/${SOURCE_VER}/config/crd/bases/source.toolkit.fluxcd.io_buckets.yaml -o $(BUCKET_CRD)

$(OCIREPO_CRD):
	curl -s https://raw.githubusercontent.com/fluxcd/source-controller/${SOURCE_VER}/config/crd/bases/source.toolkit.fluxcd.io_ocirepositories.yaml -o $(OCIREPO_CRD)

# Download the CRDs the controller depends on
download-crd-deps: $(SOURCE_CRD_VER) $(GITREPO_CRD) $(BUCKET_CRD) $(OCIREPO_CRD)

# Delete the downloaded CRD dependencies.
cleanup-crd-deps:
	rm -f $(GITREPO_CRD) $(BUCKET_CRD) $(OCIREPO_CRD)

# Install CRDs into a cluster
install: manifests
	kustomize build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests
	kustomize build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	cd config/manager && kustomize edit set image fluxcd/kustomize-controller=${IMG}
	kustomize build config/default | kubectl apply -f -

# Deploy controller dev image in the configured Kubernetes cluster in ~/.kube/config
dev-deploy: manifests
	mkdir -p config/dev && cp config/default/* config/dev
	cd config/dev && kustomize edit set image fluxcd/kustomize-controller=${IMG}
	kustomize build config/dev | kubectl apply -f -
	rm -rf config/dev

# Delete dev deployment and CRDs
dev-cleanup: manifests
	mkdir -p config/dev && cp config/default/* config/dev
	cd config/dev && kustomize edit set image fluxcd/kustomize-controller=${IMG}
	kustomize build config/dev | kubectl delete -f -
	rm -rf config/dev

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role paths="./..." output:crd:artifacts:config="config/crd/bases"
	cd api; $(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role paths="./..." output:crd:artifacts:config="../config/crd/bases"

# Generate API reference documentation
api-docs: gen-crd-api-reference-docs
	$(GEN_CRD_API_REFERENCE_DOCS) -api-dir=./api/v1 -config=./hack/api-docs/config.json -template-dir=./hack/api-docs/template -out-file=./docs/api/v1/kustomize.md

# Run go mod tidy
tidy:
	cd api; rm -f go.sum; go mod tidy -compat=1.20
	rm -f go.sum; go mod tidy -compat=1.21

# Run go fmt against code
fmt:
	go fmt ./...
	cd api; go fmt ./...

# Run go vet against code
vet:
	go vet ./...
	cd api; go vet ./...

# Generate code
generate: controller-gen
	cd api; $(CONTROLLER_GEN) object:headerFile="../hack/boilerplate.go.txt" paths="./..."

# Build the docker image
docker-build:
	docker buildx build \
	--platform=$(BUILD_PLATFORMS) \
	-t ${IMG} \
	${BUILD_ARGS} .

# Push the docker image
docker-push:
	docker push ${IMG}

# Set the docker image in-cluster
docker-deploy:
	kubectl -n flux-system set image deployment/kustomize-controller manager=${IMG}

# Find or download controller-gen
CONTROLLER_GEN = $(GOBIN)/controller-gen
.PHONY: controller-gen
controller-gen: ## Download controller-gen locally if necessary.
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION))

# Find or download gen-crd-api-reference-docs
GEN_CRD_API_REFERENCE_DOCS = $(GOBIN)/gen-crd-api-reference-docs
.PHONY: gen-crd-api-reference-docs
gen-crd-api-reference-docs: ## Download gen-crd-api-reference-docs locally if necessary
	$(call go-install-tool,$(GEN_CRD_API_REFERENCE_DOCS),github.com/ahmetb/gen-crd-api-reference-docs@$(GEN_API_REF_DOCS_VERSION))

ENVTEST = $(GOBIN)/setup-envtest
.PHONY: envtest
setup-envtest: ## Download envtest-setup locally if necessary.
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

# go-install-tool will 'go install' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-install-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(GOBIN) go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

# Build fuzzers used by oss-fuzz.
fuzz-build:
	rm -rf $(BUILD_DIR)/fuzz/
	mkdir -p $(BUILD_DIR)/fuzz/out/

	docker build . --pull --tag local-fuzzing:latest -f tests/fuzz/Dockerfile.builder
	docker run --rm \
		-e FUZZING_LANGUAGE=go -e SANITIZER=address \
		-e CIFUZZ_DEBUG='True' -e OSS_FUZZ_PROJECT_NAME=fluxcd \
		-v "$(shell go env GOMODCACHE):/root/go/pkg/mod" \
		-v "$(BUILD_DIR)/fuzz/out":/out \
		local-fuzzing:latest

# Run each fuzzer once to ensure they will work when executed by oss-fuzz.
fuzz-smoketest: fuzz-build
	docker run --rm \
		-v "$(BUILD_DIR)/fuzz/out":/out \
		-v "$(shell pwd)/tests/fuzz/oss_fuzz_run.sh":/runner.sh \
		local-fuzzing:latest \
		bash -c "/runner.sh"

# Run fuzz tests for the duration set in FUZZ_TIME.
fuzz-native: 
	KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) \
	FUZZ_TIME=$(FUZZ_TIME) \
		./tests/fuzz/native_go_run.sh
