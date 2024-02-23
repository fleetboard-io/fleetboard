
# Image URL to use all building/pushing image targets
IMG ?= ovnmaster:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:crdVersions=v1,generateEmbeddedObjectMeta=true"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

ovnmaster:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o cmd/ovnmaster/ovnmaster cmd/ovnmaster/main.go

syncer:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o cmd/syncer/syncer cmd/syncer/main.go

crossdns:
	CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -a -installsuffix cgo -o cmd/crossdns/crossdns cmd/crossdns/main.go

octopus:
	CGO_ENABLED=0 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o cmd/octopus/octopus cmd/octopus/main.go

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) paths="./..." output:crd:artifacts:config=deploy/octopus/crds/

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

dedinic:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/dedinic cmd/dedinic/main.go

ep-controller:
	CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags "-w -s" -a -installsuffix cgo -o bin/ep-controller cmd/ep-controller/main.go

images:
	docker build -f ./build/dedinic.Dockerfile ./ -t docker.io/airren/dedinic:v1.13.0-debug
	docker build -f ./build/ep-controller.Dockerfile ./ -t docker.io/airren/ep-controller:latest
	docker push docker.io/airren/dedinic:v1.13.0-debug
	docker push docker.io/airren/ep-controller:latest
