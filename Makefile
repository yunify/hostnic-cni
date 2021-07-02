tt ?= $(shell date "+%Y%m%d-%H%M")

ARCH ?= amd64
pgks ?= $(shell go list  ./pkg/... | grep -v rpc)
IMG ?= cumirror/hostnic-plus:$(tt)
#Debug level: 0, 1, 2 (1 true, 2 use bash)
DEBUG?= 0
TARGET?= default
DEPLOY?= deploy/hostnic.yaml

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
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-controller cmd/controller/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-agent cmd/ipam/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic cmd/hostnic/hostnic.go

build: vet fmt
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-controller cmd/controller/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-agent cmd/ipam/main.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic cmd/hostnic/hostnic.go

deploy:
	sed -i'' -e 's@image: .*@image: '"${IMG}"'@' config/${TARGET}/manager_image_patch.yaml
	kustomize build config/${TARGET} > ${DEPLOY}

publish: build
	docker build -t ${IMG} .
	docker push ${IMG}

generate-prototype: 
	protoc --gofast_out=plugins=grpc:. pkg/rpc/message.proto

.PHONY: deploy
