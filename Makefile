PACKAGE := github.com/travis-ci/jupiter-brain
ALL_PACKAGES := $(shell go list ./... | grep -v /vendor/)

VERSION_VAR := main.VersionString
VERSION_VALUE ?= $(shell git describe --tags --always --dirty 2> /dev/null)
REV_VAR := main.RevisionString
REV_VALUE ?= $(shell git rev-parse HEAD 2> /dev/null || echo "???")
GENERATED_VAR := main.GeneratedString
GENERATED_VALUE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%S%z')
DOCKER_IMAGE_REPO ?= travisci/jupiter-brain
DOCKER_DEST ?= $(DOCKER_IMAGE_REPO):$(VERSION_VALUE)

DOCKER ?= docker
GO ?= go
GVT ?= gvt
GOPATH := $(shell echo $${GOPATH%%:*})
GOBUILD_LDFLAGS ?= -x -ldflags "\
	-X '$(VERSION_VAR)=$(VERSION_VALUE)' \
	-X '$(REV_VAR)=$(REV_VALUE)' \
	-X '$(GENERATED_VAR)=$(GENERATED_VALUE)' \
"

COVERPROFILES := \
	server-coverage.coverprofile \
	server-negroniraven-coverage.coverprofile \
	server-jsonapi-coverage.coverprofile

%-coverage.coverprofile:
	$(GO) test -v -covermode=count -coverprofile=$@ \
    $(GOBUILD_LDFLAGS) \
    $(PACKAGE)/$(subst -,/,$(subst -coverage.coverprofile,,$@))


PORT ?= 42161
export PORT

.PHONY: all
all: clean test

.PHONY: test
test: deps build fmtpolice .test coverage.html

.PHONY: .test
.test:
	$(GO) test -v $(GOBUILD_LDFLAGS) $(ALL_PACKAGES)

.PHONY: test-race
test-race: deps
	$(GO) test -v -race $(GOBUILD_LDFLAGS) $(ALL_PACKAGES)

coverage.html: coverage.coverprofile
	$(GO) tool cover -html=$^ -o $@

coverage.coverprofile: $(COVERPROFILES)
	./utils/fold-coverprofiles $^ > $@
	$(GO) tool cover -func=$@

.PHONY: build
build: deps
	$(GO) install $(GOBUILD_LDFLAGS) $(ALL_PACKAGES)

.PHONY: docker-build
docker-build:
	$(DOCKER) build -t $(DOCKER_DEST) .

.PHONY: update
update:
	$(GB) vendor update --all

.PHONY: clean
clean:
	./utils/clean

.PHONY: distclean
distclean: clean
	rm -f vendor/.deps-fetched

.PHONY: deps
deps: vendor/.deps-fetched

vendor/.deps-fetched:
	$(GVT) rebuild
	touch $@

.PHONY: annotations
annotations:
	@git grep -E '(TODO|FIXME|XXX):' | grep -V Makefile

.PHONY: fmtpolice
fmtpolice:
	./utils/fmtpolice

.PHONY: lintall
lintall: deps
	./utils/lintall
