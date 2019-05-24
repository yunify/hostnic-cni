IMAGE_NAME ?=magicsong/hostnic:v0.0.1

ARCH ?= $(shell uname -m)

ifeq ($(ARCH),aarch64)
  ARCH = arm64
else
endif
ifeq ($(ARCH),x86_64)
  ARCH = amd64
endif

BUILD_ENV ?= GOOS=linux  GOARCH=$(ARCH)  CGO_ENABLED=0 

unit-test: fmt vet
	go test -v ./pkg/...

fmt:
	go fmt ./pkg/... ./cmd/...

vet:
	$(BUILD_ENV) go vet ./pkg/... ./cmd/...

build-binary: vet fmt
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic cmd/hostnic/hostnic.go
	$(BUILD_ENV) go build -ldflags "-w" -o bin/hostnic-agent cmd/daemon/main.go

build-docker: 
	docker build -t $(IMAGE_NAME) .
	docker push $(IMAGE_NAME)

deploy-dev:  build-binary build-docker
	kubectl apply -f deploy/hostnic.yaml