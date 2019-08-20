VERSION ?=v1.0.0-alpha2
IMAGE_NAME ?=kubesphere/hostnic
DEV_IMAGE_NAME ?=kubespheredev/hostnic
ARCH ?= $(shell uname -m)
pgks ?= $(shell go list -mod=vendor ./pkg/... | grep -v rpc)

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
	$(BUILD_ENV) go test -mod=vendor -v -coverprofile=coverage.txt -covermode=atomic  $(pgks)

fmt:
	go fmt ./pkg/... ./cmd/...

vet:
	$(BUILD_ENV) go vet ./pkg/... ./cmd/...

build-binary: vet fmt
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic cmd/hostnic/hostnic.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-agent cmd/daemon/main.go

build-docker: build-binary
	docker build -t $(IMAGE_NAME):$(VERSION) .
	docker push $(IMAGE_NAME):$(VERSION)

debug: vet fmt
	./hack/debug.sh

release-staging: docker-unit-test
	docker build -t $(DEV_IMAGE_NAME):$(VERSION) .
	docker push $(DEV_IMAGE_NAME):$(VERSION)