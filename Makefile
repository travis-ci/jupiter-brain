PACKAGE := github.com/travis-ci/jupiter-brain
SUBPACKAGES := \
	$(PACKAGE)/cmd/jb-server \
	$(PACKAGE)/server \
	$(PACKAGE)/server/jsonapi

VERSION_VAR := $(PACKAGE)/cmd/jb-server.VersionString
VERSION_VALUE ?= $(shell git describe --always --dirty 2> /dev/null)
REV_VAR := $(PACKAGE)/cmd/jb-server.RevisionString
REV_VALUE ?= $(shell git rev-parse --sq HEAD 2> /dev/null || echo "'???'")
GENERATED_VAR := $(PACKAGE)/cmd/jb-server.GeneratedString
GENERATED_VALUE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%S%z')

FIND ?= find
GO ?= deppy go
DEPPY ?= deppy
GOPATH := $(shell echo $${GOPATH%%:*})
GOBUILD_LDFLAGS ?= -ldflags "\
	-X $(VERSION_VAR) '$(VERSION_VALUE)' \
	-X $(REV_VAR) $(REV_VALUE) \
	-X $(GENERATED_VAR) '$(GENERATED_VALUE)' \
"
GOBUILD_FLAGS ?= -x

PORT ?= 42161
export PORT

.PHONY: all
all: clean test lintall

.PHONY: test
test: build fmtpolice test-deps

.PHONY: test-deps
test-deps:
	$(GO) test -i $(GOBUILD_LDFLAGS) $(PACKAGE) $(SUBPACKAGES)

.PHONY: build
build:
	$(GO) install $(GOBUILD_FLAGS) $(GOBUILD_LDFLAGS) $(PACKAGE) $(SUBPACKAGES)

.PHONY: clean
clean:
	./bin/clean

.PHONY: annotations
annotations:
	@git grep -E '(TODO|FIXME|XXX):' | grep -V Makefile

.PHONY: save
save:
	$(DEPPY) save ./...

.PHONY: fmtpolice
fmtpolice:
	./bin/fmtpolice

.PHONY: lintall
lintall:
	./bin/lintall
