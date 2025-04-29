DOCKER_REGISTRY ?= "registry.cn-hangzhou.aliyuncs.com/acs"
BINARY_NAME=ack-secret-manager
CLEANUP_NAME=ack-secret-manager-cleanup
SECRET_MANAGER_VERSION=v0.1.0
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
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cmd/manager

build-race: *.go fmt
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build -race -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cmd/manager

build-all:
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build $$(glide nv)

build-image:
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cmd/manager
	docker build --build-arg SECRET_MANAGER_VERSION=${SECRET_MANAGER_VERSION} -t ${IMG} .

build-cleanup-image:
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build -o build/bin/$(CLEANUP_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cleanup
	docker build -f cleanup/Dockerfile --build-arg SECRET_MANAGER_VERSION=${SECRET_MANAGER_VERSION} -t ${CLEANUP_IMG} .

# Run tests
test: generate fmt vet manifests
	go test -v ./backend... ./errors/... ./controllers/... -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build ${BUILD_FLAGS} -o bin/${BINARY_NAME} main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet
	go run ./main.go

# Install CRDs into a cluster
install: manifests
	kubectl apply -f config/crd/bases

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	kubectl apply -f config/crd/bases
	kustomize build config/default | kubectl apply -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) crd rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=deploy/crds

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths=./api/...

# Build the docker image
docker-build: test
	docker build --build-arg SECRET_MANAGER_VERSION=${SECRET_MANAGER_VERSION} -t ${IMG} .

# Push the docker image
docker-push:
	docker push ${IMG}

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.2.0-beta.2
CONTROLLER_GEN=$(shell go env GOPATH)/bin/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif
