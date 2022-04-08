DOCKER_REGISTRY ?= "registry.cn-hangzhou.aliyuncs.com/acs"
BINARY_NAME=ack-secret-manager-cleanup
DIR_NAME=ack-secret-manager-cleanup
SECRET_MANAGER_VERSION=v0.3.0
GO111MODULE=on
# Image URL to use all building/pushing image targets
IMG = ${DOCKER_REGISTRY}/${BINARY_NAME}:${SECRET_MANAGER_VERSION}
CLEANUP_IMG = ${DOCKER_REGISTRY}/${CLEANUP_NAME}:${SECRET_MANAGER_VERSION}
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

BUILD_FLAGS=-ldflags "-X main.version=${SECRET_MANAGER_VERSION}"

all: manager

setup:
	go get -v -u github.com/Masterminds/glide
	go get -v -u github.com/githubnemo/CompileDaemon
	go get -v -u github.com/alecthomas/gometalinter
	go get -v -u github.com/jstemmer/go-junit-report
	go get -v github.com/mattn/goveralls
	gometalinter --install --update
	glide install --strip-vendor

build: *.go fmt
	CGO_ENABLED=0  go build -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(DIR_NAME)

build-race: *.go fmt
	CGO_ENABLED=0  go build -race -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(DIR_NAME)

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...
