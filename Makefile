# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:crdVersions=v1,generateEmbeddedObjectMeta=true"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GIT_COMMIT = $(shell git rev-parse HEAD)
ifeq ($(shell git tag --points-at ${GIT_COMMIT}),)
GIT_VERSION=$(shell echo ${GIT_COMMIT} | cut -c 1-7)
else
GIT_VERSION=$(shell git describe --abbrev=0 --tags --always)
endif

IMAGE_TAG = ${GIT_VERSION}
REGISTRY ?= ghcr.io
REGISTRY_NAMESPACE ?= nauti-io


DOCKERARGS?=
ifdef HTTP_PROXY
	DOCKERARGS += --build-arg http_proxy=$(HTTP_PROXY)
endif
ifdef HTTPS_PROXY
	DOCKERARGS += --build-arg https_proxy=$(HTTPS_PROXY)
endif

lint: golangci-lint
	golangci-lint run -c .golangci.yaml --timeout=10m


# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./..." output:crd:artifacts:config=deploy/hub/crds/

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.8.0 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif


crossdns:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o bin/crossdns cmd/crossdns/main.go

octopus:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/octopus cmd/octopus/main.go

cnf:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/cnf cmd/cnf/main.go


ep-controller:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/ep-controller cmd/ep-controller/main.go

images:
	docker build $(DOCKERARGS) -f ./build/crossdns.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}
	docker build $(DOCKERARGS) -f ./build/octopus.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/octopus:${IMAGE_TAG}
	docker build $(DOCKERARGS) -f ./build/dedinic.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/dedinic:${IMAGE_TAG}
	docker build $(DOCKERARGS) -f ./build/ep-controller.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/ep-controller:${IMAGE_TAG}

image-crossdns:
	docker build $(DOCKERARGS) -f ./build/crossdns.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}

image-octopus:
	docker build $(DOCKERARGS) -f ./build/octopus.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/octopus:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/octopus:${IMAGE_TAG}

image-dedinic:
	docker build $(DOCKERARGS) -f ./build/dedinic.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/dedinic:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/dedinic:${IMAGE_TAG}

image-cnf:
	docker build $(DOCKERARGS) -f ./build/cnf.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG}
	docker tag ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG} ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:latest
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:latest

image-ep-controller:
	docker build $(DOCKERARGS) -f ./build/ep-controller.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/ep-controller:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/ep-controller:${IMAGE_TAG}


images-push:
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/octopus:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/dedinic:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/ep-controller:${IMAGE_TAG}

# find or download golangci-lint
# download golangci-lint if necessary
golangci-lint:
ifeq (, $(shell which golangci-lint))
	@{ \
	set -e ;\
	export GO111MODULE=on; \
	GOLANG_LINT_TMP_DIR=$$(mktemp -d) ;\
	cd $$GOLANG_LINT_TMP_DIR ;\
	go mod init tmp ;\
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.56.2 ;\
	rm -rf $$GOLANG_LINT_TMP_DIR ;\
	}
GOLANG_LINT=$(shell go env GOPATH)/bin/golangci-lint
else
GOLANG_LINT=$(shell which golangci-lint)
endif
