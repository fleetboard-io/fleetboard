
# Image URL to use all building/pushing image targets
IMG ?= ovnmaster:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:crdVersions=v1,generateEmbeddedObjectMeta=true"

IMAGE_TAG := $(shell git rev-parse --short HEAD)
IMAGE_REPOSITORY := ghcr.io/nauti-io


# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
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


ovnmaster:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o cmd/ovnmaster/ovnmaster cmd/ovnmaster/main.go
crossdns:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o cmd/crossdns/crossdns cmd/crossdns/main.go
octopus:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o cmd/octopus/octopus cmd/octopus/main.go
dedinic:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/dedinic cmd/dedinic/main.go
ep-controller:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/ep-controller cmd/ep-controller/main.go

images:
	docker build -f ./build/dedinic.Dockerfile ./ -t ${IMAGE_REPOSITORY}/dedinic:${IMAGE_TAG}
	docker build -f ./build/ep-controller.Dockerfile ./ -t ${IMAGE_REPOSITORY}/ep-controller:${IMAGE_TAG}
	docker push ${IMAGE_REPOSITORY}/dedinic:${IMAGE_TAG}
	docker push ${IMAGE_REPOSITORY}/ep-controller:${IMAGE_TAG}

dedinic-image:
	docker build -f ./build/dedinic.Dockerfile ./ -t${IMAGE_REPOSITORY}/dedinic:${IMAGE_TAG}
	docker push ${IMAGE_REPOSITORY}/dedinic:${IMAGE_TAG}
ep-controller-image:
	docker build -f ./build/ep-controller.Dockerfile ./ -t ${IMAGE_REPOSITORY}/ep-controller:${IMAGE_TAG}
	docker push ${IMAGE_REPOSITORY}/ep-controller:${IMAGE_TAG}


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
