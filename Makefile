REGISTRY ?= quay.io 
IMAGE_TAG ?= latest

BIN_DIR = ${PWD}/build/_output/bin/
export GOPROXY=direct
export GOFLAGS=-mod=vendor
export GOROOT=${BIN_DIR}/go/
export GOBIN=${GOROOT}/bin/
export PATH := ${GOROOT}/bin:${PATH}
export GO := ${GOBIN}/go
OCI_BIN ?= $(shell if hash podman 2>/dev/null; then echo podman; elif hash docker 2>/dev/null; then echo docker; fi)
TLS_SETTING := $(if $(filter $(OCI_BIN),podman),"--tls-verify=false",)

GINKGO ?= $(GOBIN)/ginkgo

COMPONENTS = $(sort \
			 $(subst /,-,\
			 $(patsubst cmd/%/,%,\
			 $(dir \
			 $(shell find cmd/ -type f -name '*.go')))))

all: build

$(GO):
	hack/install-go.sh $(BIN_DIR)

$(GINKGO): go.mod
	$(MAKE) tools

build: marker format

format: $(GO)
	$(GO) fmt ./pkg/... ./cmd/... ./tests/...
	$(GO) vet ./pkg/... ./cmd/... ./tests/...

functest: $(GINKGO)
	GINKGO=$(GINKGO) hack/build-func-tests.sh
	GINKGO=$(GINKGO) hack/functests.sh

marker: 
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/bridge-marker github.com/Acedus/bridge-marker-dp/cmd/marker

docker-build: marker
	$(OCI_BIN) build -t ${REGISTRY}/bridge-marker:${IMAGE_TAG} ./build

docker-push:
	$(OCI_BIN) push ${TLS_SETTING} ${REGISTRY}/bridge-marker:${IMAGE_TAG}
	# $(OCI_BIN) tag ${REGISTRY}/bridge-marker:${IMAGE_TAG} ${REGISTRY}/bridge-marker:${IMAGE_GIT_TAG}
	# $(OCI_BIN) push ${TLS_SETTING} ${REGISTRY}/bridge-marker:${IMAGE_GIT_TAG}

cluster-sync: build
	./cluster/sync.sh

vendor: $(GO)
	$(GO) mod tidy
	$(GO) mod vendor

tools: $(GO)
	./hack/install-tools.sh

.PHONY: build format docker-build docker-push cluster-sync vendor marker tools
