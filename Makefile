DOCKER_REGISTRY ?= "registry.cn-hangzhou.aliyuncs.com/acs"
BINARY_NAME=ack-secret-manager
CLEANUP_NAME=ack-secret-manager-cleanup
SECRET_MANAGER_VERSION=v0.6.7
GO111MODULE=on
# Image URL to use all building/pushing image targets
IMG = ${DOCKER_REGISTRY}/${BINARY_NAME}:${SECRET_MANAGER_VERSION}
CLEANUP_IMG = ${DOCKER_REGISTRY}/${CLEANUP_NAME}:${SECRET_MANAGER_VERSION}

BUILD_FLAGS=-ldflags "-X github.com/AliyunContainerService/ack-secret-manager/version.Version=${SECRET_MANAGER_VERSION}"

all: manager

build: fmt
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build ${BUILD_FLAGS} -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cmd/manager

# NOTE: -race requires cgo; on Windows hosts without gcc this target will fail.
build-race: fmt
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build ${BUILD_FLAGS} -race -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cmd/manager

build-all:
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build ./...

build-image:
	GOARCH=${TARGETARCH} GOOS=${TARGETOS} CGO_ENABLED=0  go build ${BUILD_FLAGS} -o build/bin/$(BINARY_NAME) github.com/AliyunContainerService/$(BINARY_NAME)/cmd/manager
	docker build --build-arg SECRET_MANAGER_VERSION=${SECRET_MANAGER_VERSION} -t ${IMG} .

# Run tests (aligned with CI: go test ./pkg/..., without -race for non-cgo local builds)
test: generate fmt vet manifests
	go test -count=1 ./pkg/... -coverprofile cover.out

# Build manager binary
manager: generate fmt vet
	go build ${BUILD_FLAGS} -o bin/${BINARY_NAME} ./cmd/manager

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet
	go run ./cmd/manager

# Install CRDs into a cluster (the sample CR in deploy/crds is intentionally excluded)
install: manifests
	kubectl apply -f deploy/crds/alibabacloud.com_externalsecrets.yaml \
		-f deploy/crds/alibabacloud.com_secretstores.yaml \
		-f deploy/crds/alibabacloud.com_clusterexternalsecrets.yaml \
		-f deploy/crds/alibabacloud.com_clustersecretstores.yaml

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	kubectl apply -f deploy/crds/alibabacloud.com_externalsecrets.yaml \
		-f deploy/crds/alibabacloud.com_secretstores.yaml \
		-f deploy/crds/alibabacloud.com_clusterexternalsecrets.yaml \
		-f deploy/crds/alibabacloud.com_clustersecretstores.yaml
	kubectl apply -f deploy/service_account.yaml -f deploy/role.yaml -f deploy/role_binding.yaml -f deploy/operator.yaml

# Generate CRD manifests.
manifests: controller-gen
	$(CONTROLLER_GEN) crd rbac:roleName=manager-role paths="./pkg/apis/alibabacloud/v1alpha1/" output:crd:artifacts:config=deploy/crds

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths=./pkg/apis/alibabacloud/v1alpha1

# Build the docker image
docker-build: test
	docker build --build-arg SECRET_MANAGER_VERSION=${SECRET_MANAGER_VERSION} -t ${IMG} .

# Push the docker image
docker-push:
	docker push ${IMG}

# find or download controller-gen
CONTROLLER_GEN_VERSION ?= v0.16.5
controller-gen:
ifeq (, $(shell which controller-gen))
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
CONTROLLER_GEN=$(shell go env GOPATH)/bin/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

.PHONY: test-e2e
test-e2e:
	@echo "Running E2E tests..."
	go test ./test/e2e/... -v -ginkgo.v -timeout 3h
test-e2e-template:
	@echo "Running Template Processing E2E tests..."
	go test ./test/e2e -v -ginkgo.v -timeout 3h -ginkgo.focus="Template Processing E2E"
