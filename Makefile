# asustor-realfanctrl — Makefile
#
# Author:     Christoph C. Cemper / Magosol Kft.
# Repository: https://github.com/christophcemper/asustor-realfanctrl
# License:    MIT — see LICENSE.
#
# NO GUARANTEES: this software drives cooling hardware and has only been tested
# on ASUSTOR AS6812F / ADM 5.1.3.RI81. See README.md before deploying.
#
# Common use:
#   make                        build for the NAS (linux/amd64)
#   make check                  fmt + vet + test
#   make deploy NAS=cccnas6     build, copy, install and restart on one NAS
#   make status NAS=cccnas6     show that NAS's sensors and fan state
#   make help                   list every target

BINARY      := realfanctrld
PKG         := .
DIST        := dist

# ASUSTOR ADM on the AS6812F is x86_64 Linux. Static build: the NAS has no
# usable libc toolchain and CGO would tie us to the build host's glibc.
GOOS        ?= linux
GOARCH      ?= amd64
CGO_ENABLED ?= 0

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILT       ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.built=$(BUILT)

GOFLAGS := -trimpath

# Remote deployment. Override on the command line: make deploy NAS=cccnas7
NAS         ?=
SSH         ?= ssh
SCP         ?= scp
REMOTE_TMP  ?= ~/
INITD       := /usr/local/etc/init.d/S60realfanctrld
REMOTE_BIN  := /usr/local/bin/$(BINARY)
REMOTE_CLI  := /usr/local/bin/realfanctrl
REMOTE_CONF := /usr/local/etc/realfanctrl.conf

.DEFAULT_GOAL := build
.PHONY: all build check fmt fmt-check vet test lint clean dist install-local \
        deploy push-files remote-install status logs restart uninstall help

## build: compile the daemon for the NAS (linux/amd64, static)
build:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "built $(BINARY) $(VERSION) for $(GOOS)/$(GOARCH)"

## all: run every check, then build
all: check build

## check: gofmt, go vet and tests — what CI should run
check: fmt-check vet test

## fmt: rewrite sources with gofmt
fmt:
	gofmt -w .

## fmt-check: fail if any file is not gofmt-clean
fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@echo "gofmt clean"

## vet: run go vet
vet:
	go vet ./...

## test: run the test suite
test:
	go test ./...

## lint: run golangci-lint when it is installed (optional)
lint:
	@command -v golangci-lint >/dev/null 2>&1 \
		&& golangci-lint run \
		|| echo "golangci-lint not installed, skipping"

## dist: build a release tarball with the binary, init script and docs
dist: build
	@mkdir -p $(DIST)
	@tar czf $(DIST)/asustor-realfanctrl-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz \
		$(BINARY) bin/realfanctrl init.d/S60realfanctrld .bash_aliases \
		README.md INSTALL.md ASUSTOR-DEFECTS.md LICENSE examples
	@echo "wrote $(DIST)/asustor-realfanctrl-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz"

## clean: remove build artefacts
clean:
	rm -rf $(BINARY) $(DIST)

# --- remote targets --------------------------------------------------------
# All of these need NAS=<ssh-host> and will prompt for the NAS sudo password.

guard-nas:
	@if [ -z "$(NAS)" ]; then \
		echo "set NAS=<ssh-host>, e.g. make deploy NAS=cccnas6"; exit 1; fi

## push-files: copy binary, CLI, init script and aliases to NAS:~ (no sudo)
push-files: guard-nas build
	$(SCP) -q $(BINARY) bin/realfanctrl init.d/S60realfanctrld .bash_aliases $(NAS):$(REMOTE_TMP)
	@echo "copied to $(NAS):$(REMOTE_TMP)"

## remote-install: install from NAS:~ into /usr/local and (re)start the daemon
remote-install: guard-nas
	$(SSH) -t $(NAS) '\
		sudo install -m 755 $(REMOTE_TMP)$(BINARY) $(REMOTE_BIN) && \
		sudo install -m 755 $(REMOTE_TMP)realfanctrl $(REMOTE_CLI) && \
		sudo install -m 755 $(REMOTE_TMP)S60realfanctrld $(INITD) && \
		[ -f $(REMOTE_CONF) ] || sudo $(REMOTE_BIN) -write-config && \
		sudo $(INITD) restart'

## deploy: build, copy and install on NAS in one step
deploy: push-files remote-install status

## status: sensors, curve target and real fan RPM on NAS
status: guard-nas
	$(SSH) -t $(NAS) 'sudo $(INITD) status'

## logs: follow the daemon log on NAS
logs: guard-nas
	$(SSH) -t $(NAS) 'sudo tail -f /usr/local/var/log/realfanctrld.log'

## restart: restart the daemon on NAS (use after editing the config)
restart: guard-nas
	$(SSH) -t $(NAS) 'sudo $(INITD) restart'

## check-adm: audit ADM's own fan settings on NAS (exit 1 if they differ)
check-adm: guard-nas
	$(SSH) -t $(NAS) 'sudo $(REMOTE_BIN) -check'

## apply-adm: apply the recommended ADM fan settings on NAS and restart emboardmand
apply-adm: guard-nas
	$(SSH) -t $(NAS) 'sudo $(REMOTE_BIN) -apply-config'

## uninstall: stop and remove the daemon from NAS (ADM resumes fan control)
uninstall: guard-nas
	$(SSH) -t $(NAS) '\
		sudo $(INITD) stop; \
		sudo rm -f $(INITD) $(REMOTE_BIN) $(REMOTE_CLI); \
		echo "removed. $(REMOTE_CONF) kept; delete it manually if unwanted."'

## help: list available targets
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | sort
