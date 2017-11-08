# tab space is 4
# GitHub viewer defaults to 8, change with ?ts=4 in URL

# Vars describing project
NAME= hostnic-cni
GIT_REPOSITORY= github.com/yunify/hostnic-cni
DOCKER_IMAGE_NAME?= qingcloud/hostnic-cni
GOPATH ?= $(HOME)/go/
BASE= $(GOPATH)/src/$(GIT_REPOSITORY)
BIN      = $(GOPATH)/bin

export GOPATH
GO      = go
GOFMT   = gofmt
TIMEOUT = 15
V = 0
Q = $(if $(filter 1,$V),,@)
M = $(shell printf "\033[34;1m▶\033[0m")

# Generate vars to be included from external script
# Allows using bash to generate complex vars, such as project versions
GENERATE_VERSION_INFO_SCRIPT	= ./generate_version.sh
GENERATE_VERSION_INFO_OUTPUT	= version_info

# Define newline needed for subsitution to allow evaluating multiline script output
define newline


endef

# Call the version_info script with keyvalue option and evaluate the output
# Will import the keyvalue pairs and make available as Makefile variables
# Use dummy variable to only have execute once
$(eval $(subst #,$(newline),$(shell $(GENERATE_VERSION_INFO_SCRIPT) keyvalue | tr '\n' '#')))
# Call the verson_info script with json option and store result into output file and variable
# Will only execute once due to ':='
#GENERATE_VERSION_INFO			:= $(shell $(GENERATE_VERSION_INFO_SCRIPT) json | tee $(GENERATE_VERSION_INFO_OUTPUT))

# Set defaults for needed vars in case version_info script did not set
# Revision set to number of commits ahead
VERSION							?= 0.0
COMMITS							?= 0
REVISION						?= $(COMMITS)
BUILD_LABEL						?= unknown_build
BUILD_DATE						?= $(shell date -u +%Y%m%d.%H%M%S)
GIT_SHA1						?= unknown_sha1

IMAGE_LABLE         ?= $(BUILD_LABEL)
# Vars for export ; generate list of ENV vars based on matching export prefix
# Use strip to get rid of excessive spaces due to the foreach / filter / if logic
EXPORT_VAR_PREFIX               = EXPORT_VAR_
EXPORT_VARS                     = $(strip $(foreach v,$(filter $(EXPORT_VAR_PREFIX)%,$(.VARIABLES)),$(if $(filter environment%,$(origin $(v))),$(v))))

# Vars for go phase
# All vars which being with prefix will be included in ldflags
# Defaulting to full static build
GO_VARIABLE_PREFIX				= GO_VAR_
GO_VAR_BUILD_LABEL				:= $(BUILD_LABEL)
GO_VAR_VERSION                  := $(VERSION)
GO_VAR_GIT_SHA1                 := $(GIT_SHA1)
GO_VAR_BUILD_LABEL              := $(BUILD_LABEL)
GO_LDFLAGS						= $(foreach v,$(filter $(GO_VARIABLE_PREFIX)%, $(.VARIABLES)),-X github.com/yunify/hostnic-cni/pkg.$(patsubst $(GO_VARIABLE_PREFIX)%,%,$(v))=$(value $(value v)))
GO_BUILD_FLAGS					= -a -tags netgo -installsuffix nocgo -ldflags "$(GO_LDFLAGS)"

#src
PKGS     = $(or $(PKG),$(shell cd $(BASE) && env GOPATH=$(GOPATH) $(GO) list ./... | grep -v "^$(PACKAGE)/vendor/"))

hostnic_pkg = $(subst $(GIT_REPOSITORY)/,,$(shell go list -f '{{ join .Deps "\n" }}' $(GIT_REPOSITORY)/cmd/hostnic | grep "^$(GIT_REPOSITORY)" |grep -v "^$(GIT_REPOSITORY)/vendor/" ))
hostnic_pkg += cmd/hostnic

daemon_pkg = $(subst $(GIT_REPOSITORY)/,,$(shell go list -f '{{ join .Deps "\n" }}' $(GIT_REPOSITORY)/cmd/daemon | grep "^$(GIT_REPOSITORY)" |grep -v "^$(GIT_REPOSITORY)/vendor/" ))
daemon_pkg += cmd/daemon

# Define targets
TESTPKGS = $(shell env GOPATH=$(GOPATH) $(GO) list -f '{{ if or .TestGoFiles .XTestGoFiles }}{{ .ImportPath }}{{ end }}' $(PKGS))
# default just build binary
all  							: $(BASE) go-build ; $(info $(M) executable is built …)

# target for debugging / printing variables
print-%							:
								@echo '$*=$($*)'

# perform go build on project
go-build						: bin/hostnic bin/daemon

bin/hostnic                     : fmt vendor |$(foreach dir,$(hostnic_pkg),$(wildcard $(dir)/*.go)) Makefile
								@go build -o bin/hostnic $(GO_BUILD_FLAGS) $(GIT_REPOSITORY)/cmd/hostnic/

bin/hostnic.tar.gz              : bin/hostnic
								tar -C bin/ -czf bin/hostnic.tar.gz hostnic

bin/daemon                      : fmt vendor |$(foreach dir,$(daemon_pkg),$(wildcard $(dir)/*.go)) Makefile
								@go build -o bin/daemon $(GO_BUILD_FLAGS) $(GIT_REPOSITORY)/cmd/daemon/

bin/.docker-images-build-timestamp   : $(foreach dir,$(daemon_pkg),$(wildcard $(dir)/*.go)) Dockerfile
								mkdir -p bin
								docker build -q -t $(DOCKER_IMAGE_NAME):$(IMAGE_LABLE) -t dockerhub.qingcloud.com/$(DOCKER_IMAGE_NAME):$(IMAGE_LABLE) . > bin/.docker-images-build-timestamp

release                         : bin/hostnic.tar.gz test bin/.docker-images-build-timestamp

bin/.docker_label               : bin/.docker-images-build-timestamp
								docker push $(DOCKER_IMAGE_NAME):$(IMAGE_LABLE)
								docker push dockerhub.qingcloud.com/$(DOCKER_IMAGE_NAME):$(IMAGE_LABLE)
								echo $(DOCKER_IMAGE_NAME):$(IMAGE_LABLE) > bin/.docker_label

install-docker                  : bin/.docker_label

publish                         : bin/.docker_label
								docker tag `cat bin/.docker_label` $(DOCKER_IMAGE_NAME):latest
								docker tag `cat bin/.docker_label` dockerhub.qingcloud.com/$(DOCKER_IMAGE_NAME):latest
								docker push $(DOCKER_IMAGE_NAME):latest
								docker push dockerhub.qingcloud.com/$(DOCKER_IMAGE_NAME):latest

install-distrib                 : go-build
								cp bin/daemon /usr/local/bin/hostnic-daemon
								cp distrib/hostnic.service /etc/system/systemd/
								cp distrib/hostnic.conf /etc/qingcloud/hostnic.conf
								systemctl daemon-reload

clean                           :
								rm -rf bin/

$(BASE): ; $(info $(M) setting GOPATH…)
	@mkdir -p $(dir $@)
	@ln -sf $(CURDIR) $@

$(BIN):
	@mkdir -p $@
$(BIN)/%: $(BIN) | $(BASE) ; $(info $(M) building $(REPOSITORY)…)
	$Q tmp=$$(mktemp -d); \
		(GOPATH=$$tmp go get $(REPOSITORY) && cp $$tmp/bin/* $(BIN)/.) || ret=$$?; \
		rm -rf $$tmp ; exit $$ret
GODEP = $(BIN)/dep
$(BIN)/dep: REPOSITORY=github.com/golang/dep/cmd/dep

GOLINT = $(BIN)/golint
$(BIN)/golint: REPOSITORY=github.com/golang/lint/golint
# Dependency management

vendor: Gopkg.toml Gopkg.lock | $(BASE) $(GODEP) ; $(info $(M) retrieving dependencies…)
	$Q cd $(BASE) && $(GODEP) ensure
	@touch $@
.PHONY: vendor-update
vendor-update: vendor | $(BASE) $(GODEP)
ifeq "$(origin PKG)" "command line"
	$(info $(M) updating $(PKG) dependency…)
	$Q cd $(BASE) && $(GODEP) ensure -update $(PKG)
else
	$(info $(M) updating all dependencies…)
	$Q cd $(BASE) && $(GODEP) ensure -update
endif
	@ln -nsf . vendor/src
	@touch vendor

TEST_TARGETS := test-default test-bench test-short test-verbose test-race
.PHONY: $(TEST_TARGETS) check test tests
test-bench:   ARGS=-run=__absolutelynothing__ -bench=. ## Run benchmarks
test-short:   ARGS=-short        ## Run only short tests
test-verbose: ARGS=-v            ## Run tests in verbose mode with coverage reporting
test-race:    ARGS=-race         ## Run tests with race detector
$(TEST_TARGETS): NAME=$(MAKECMDGOALS:test-%=%)
$(TEST_TARGETS): test
check test tests: fmt vendor | $(BASE) ; $(info $(M) running $(NAME:%=% )tests…) @ ## Run tests

	$Q cd $(BASE) && $(GO) test -i $(TESTPKGS) && $(GO) test -timeout $(TIMEOUT)s $(ARGS) $(TESTPKGS)

.PHONY: lint
lint: vendor | $(BASE) $(GOLINT) ; $(info $(M) running golint…) @ ## Run golint
	$Q cd $(BASE) && ret=0 && for pkg in $(PKGS); do \
		test -z "$$($(GOLINT) $$pkg | tee /dev/stderr)" || ret=1 ; \
	 done ; exit $$ret

.PHONY: fmt
fmt: ; $(info $(M) running gofmt…) @ ## Run gofmt on all source files
	@ret=0 && for d in $$($(GO) list -f '{{.Dir}}' ./... | grep -v /vendor/); do \
		$(GOFMT) -l -w $$d/*.go || ret=$$? ; \
	 done ; exit $$ret

.PHONY							: default all go-build clean release install install-distrib install-docker test


