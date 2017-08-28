# tab space is 4
# GitHub viewer defaults to 8, change with ?ts=4 in URL

# Vars describing project
NAME= hostnic-cni
GIT_REPOSITORY= github.com/yunify/hostnic-cni
DOCKER_IMAGE_NAME?= dockerhub.qingcloud.com/qingcloud/hostnic-cni

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
hostnic_pkg = $(subst $(GIT_REPOSITORY)/,,$(shell go list -f '{{ join .Deps "\n" }}' $(GIT_REPOSITORY)/cmd/hostnic | grep "^$(GIT_REPOSITORY)" |grep -v "^$(GIT_REPOSITORY)/vendor/" ))
hostnic_pkg += cmd/hostnic

daemon_pkg = $(subst $(GIT_REPOSITORY)/,,$(shell go list -f '{{ join .Deps "\n" }}' $(GIT_REPOSITORY)/cmd/daemon | grep "^$(GIT_REPOSITORY)" |grep -v "^$(GIT_REPOSITORY)/vendor/" ))
daemon_pkg += cmd/daemon

# Define targets
TEST_PACKAGES?=cmd pkg
# default just build binary
default							: go-build

# target for debugging / printing variables
print-%							:
								@echo '$*=$($*)'

# perform go build on project
go-build						: bin/hostnic bin/daemon

bin/hostnic                     : $(foreach dir,$(hostnic_pkg),$(wildcard $(dir)/*.go))
								go build -o bin/hostnic $(GO_BUILD_FLAGS) $(GIT_REPOSITORY)/cmd/hostnic/

bin/hostnic.tar.gz              : bin/hostnic
								tar -C bin/ -czf bin/hostnic.tar.gz hostnic

bin/daemon                      : $(foreach dir,$(daemon_pkg),$(wildcard $(dir)/*.go))
								go build -o bin/daemon $(GO_BUILD_FLAGS) $(GIT_REPOSITORY)/cmd/daemon/

bin/.docker-images-build-timestamp   : bin/daemon Dockerfile
								cp -u Dockerfile bin
								docker build -q -t $(DOCKER_IMAGE_NAME):$(BUILD_LABEL) bin/ > bin/.docker-images-build-timestamp

release                         : test bin/hostnic.tar.gz bin/.docker-images-build-timestamp

install                         : release
								docker push $(DOCKER_IMAGE_NAME):$(BUILD_LABEL)

clean                           :
								docker rmi `cat bin/.docker-images-build-timestamp`
								rm -rf bin/

.PHONY							: default all go-build clean release install test
