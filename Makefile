PACKAGE := github.com/travis-ci/jupiter-brain
PACKAGE_SRC_DIR := src/$(PACKAGE)
SUBPACKAGES := \
	$(PACKAGE)/cmd/jb-server \
	$(PACKAGE)/server \
	$(PACKAGE)/server/jsonapi

VERSION_VAR := main.VersionString
VERSION_VALUE ?= $(shell git describe --always --dirty 2> /dev/null)
REV_VAR := main.RevisionString
REV_VALUE ?= $(shell git rev-parse --sq HEAD 2> /dev/null || echo "'???'")
GENERATED_VAR := main.GeneratedString
GENERATED_VALUE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%S%z')

GB ?= gb
GOPATH := $(PWD):$(PWD)/vendor:$(shell echo $${GOPATH%%:*})
GOBUILD_LDFLAGS ?= -ldflags "\
	-X $(VERSION_VAR) '$(VERSION_VALUE)' \
	-X $(REV_VAR) $(REV_VALUE) \
	-X $(GENERATED_VAR) '$(GENERATED_VALUE)' \
"

PORT ?= 42161
export PORT

.PHONY: all
all: clean test

.PHONY: test
test: lintall build fmtpolice .test

.PHONY: .test
.test:
	$(GB) test -v

.PHONY: build
build:
	$(GB) build $(GOBUILD_LDFLAGS)

.PHONY: update
update:
	$(GB) vendor update --all

.PHONY: clean
clean:
	./utils/clean

.PHONY: annotations
annotations:
	@git grep -E '(TODO|FIXME|XXX):' | grep -V Makefile

.PHONY: fmtpolice
fmtpolice:
	./utils/fmtpolice $(PACKAGE_SRC_DIR)

.PHONY: lintall
lintall:
	./utils/lintall
