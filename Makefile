ARCH ?= $(shell uname -m)
pgks ?= $(shell go list  ./pkg/... | grep -v rpc)
#Debug level: 0, 1, 2 (1 true, 2 use bash)
DEBUG?= 0
TAG ?= $(shell git describe --tags)
IMG ?= qingcloud/hostnic-plus:$(TAG)
TARGET ?= default
DEPLOY ?= deploy/hostnic.yml
REPO=qingcloud

ifneq ($(DEBUG), 0)
	TARGET = dev
endif

ifeq ($(ARCH),aarch64)
  ARCH = arm64
else
endif
ifeq ($(ARCH),x86_64)
  ARCH = amd64
endif

BUILD_ENV ?= GOOS=linux  GOARCH=$(ARCH)  CGO_ENABLED=0
CORE_BIN_DIR ?= bin/hostnic
TOOLS_BIN_DIR ?= bin/hostnic/tools
WEBHOOK_BIN_DIR ?= bin/webhook

docker-unit-test: fmt vet
	docker run --rm -e GO111MODULE=on  -v "${PWD}":/root/myapp -w /root/myapp golang:1.12 make unit-test 

unit-test:
	$(BUILD_ENV) go test -v -coverprofile=coverage.txt -covermode=atomic  $(pgks)

fmt:
	go fmt ./pkg/... ./cmd/...

vet:
	$(BUILD_ENV) go vet ./pkg/... ./cmd/...

# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

# Generate manifests e.g. CRD, RBAC etc.
manifests:
	go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go object:headerFile=./hack/boilerplate.go.txt paths=./pkg/apis/... rbac:roleName=controller-perms ${CRD_OPTIONS} output:crd:artifacts:config=config/crds

# find or download controller-gen
# download controller-gen if necessary
clientset:
	./hack/generate_client.sh

rebuild: clientset manifests vet fmt
	$(BUILD_ENV) go build -ldflags "-w" -o $(CORE_BIN_DIR)/hostnic-controller cmd/controller/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(CORE_BIN_DIR)/hostnic-agent cmd/ipam/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(CORE_BIN_DIR)/hostnic cmd/hostnic/hostnic.go

build: vet fmt
	$(BUILD_ENV) go build -ldflags "-w" -o $(CORE_BIN_DIR)/hostnic-controller cmd/controller/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(CORE_BIN_DIR)/hostnic-agent cmd/ipam/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(CORE_BIN_DIR)/hostnic cmd/hostnic/hostnic.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(WEBHOOK_BIN_DIR)/hostnic-webhook cmd/webhook/main.go cmd/webhook/webhook.go

tools: vet fmt
	#$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/db-client cmd/db-client/client.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/ipam-client cmd/tools/ipam-client/client.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/hostnic-client cmd/tools/hostnic-client/client.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/vxnet-client cmd/tools/vxnet-client/client.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/patch-node cmd/tools/node-patch/patch.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/dhcp-client cmd/tools/dhcp-client/client.go
	$(BUILD_ENV) go build -ldflags "-w" -o $(TOOLS_BIN_DIR)/ipam-check cmd/tools/ipam-check/check.go

deploy:
	sed -i'' -e 's@image: .*@image: '"${IMG}"'@' config/${TARGET}/manager_image_patch.yaml
	kustomize build config/${TARGET} > ${DEPLOY}

publish: build tools
	hack/docker_build.sh ${REPO} ${TAG}

generate-prototype: 
	protoc --go_out=. pkg/rpc/message.proto
	protoc --go-grpc_out=require_unimplemented_servers=false:. pkg/rpc/message.proto

.PHONY: deploy

push:
	docker buildx build --build-arg CORE_BIN_DIR=${CORE_BIN_DIR} --build-arg TOOLS_BIN_DIR=${TOOLS_BIN_DIR} -t ${REPO}/hostnic-plus:${TAG} --platform=linux/amd64,linux/arm64 -f build/hostnic/DockerfileWithBuilder . --push
	docker buildx build --build-arg WEBHOOK_BIN_DIR=${WEBHOOK_BIN_DIR} -t ${REPO}/hostnic-webhook:${TAG} --platform=linux/amd64,linux/arm64 -f build/webhook/DockerfileWithBuilder . --push