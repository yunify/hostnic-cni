ARCH ?= $(shell uname -m)
pgks ?= $(shell go list  ./pkg/... | grep -v rpc)
IMG ?= kubespheredev/hostnic:v1.0.0-beta
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

build: vet fmt
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic cmd/hostnic/hostnic.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-agent cmd/daemon/main.go

deploy:
	sed -i'' -e 's@image: .*@image: '"${IMG}"'@' config/${TARGET}/manager_image_patch.yaml
	kustomize build config/${TARGET} > ${DEPLOY}

publish: build deploy
	docker build -t ${IMG} .
	docker push ${IMG}

generate-prototype: 
	protoc --gofast_out=plugins=grpc:. pkg/rpc/message.proto

.PHONY: deploy