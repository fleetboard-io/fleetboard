
# Image URL to use all building/pushing image targets
IMG ?= ovnmaster:latest
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
