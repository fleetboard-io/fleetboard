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
REGISTRY_NAMESPACE ?= fleetboard-io


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


all: crossdns cnf proxy ep-controller

crossdns:
	CGO_ENABLED=0 go build -ldflags="-s -w" -a -installsuffix cgo -o bin/crossdns cmd/crossdns/main.go

cnf:
	CGO_ENABLED=0 go build -ldflags "-w -s" -a -installsuffix cgo -o bin/cnf cmd/cnf/main.go

proxy:
	CGO_ENABLED=0 go build -ldflags "-w -s" -a -installsuffix cgo -o bin/proxy cmd/proxy/main.go

ep-controller:
	CGO_ENABLED=0 go build -ldflags "-w -s" -a -installsuffix cgo -o bin/ep-controller cmd/ep-controller/main.go

images:
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/crossdns.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/cnf.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG}
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/ep-controller.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/controller:${IMAGE_TAG}
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/proxy.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/proxy:${IMAGE_TAG}

image-crossdns:
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/crossdns.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG}
	docker tag ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:${IMAGE_TAG} ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:latest
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/crossdns:latest

image-cnf:
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/cnf.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG}
	docker tag ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:${IMAGE_TAG} ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:latest
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/cnf:latest

image-proxy:
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/proxy.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/proxy:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/proxy:${IMAGE_TAG}
	docker tag ${REGISTRY}/${REGISTRY_NAMESPACE}/proxy:${IMAGE_TAG} ${REGISTRY}/${REGISTRY_NAMESPACE}/proxy:latest
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/proxy:latest

image-ep-controller:
	docker buildx build --platform linux/amd64,linux/arm64 $(DOCKERARGS) -f ./build/ep-controller.Dockerfile ./ -t ${REGISTRY}/${REGISTRY_NAMESPACE}/controller:${IMAGE_TAG}
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/controller:${IMAGE_TAG}
	docker tag ${REGISTRY}/${REGISTRY_NAMESPACE}/controller:${IMAGE_TAG} ${REGISTRY}/${REGISTRY_NAMESPACE}/controller:latest
	docker push ${REGISTRY}/${REGISTRY_NAMESPACE}/controller:latest


images-push: image-crossdns image-cnf image-proxy image-ep-controller

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
