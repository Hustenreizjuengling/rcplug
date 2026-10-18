# rcplug: plugin host, console and SDK for the RØDECaster (aarch64 Linux).
# Pure Go standard library, so cross-compiling needs no C toolchain.
# Run "make help" for the targets.

GO       ?= go
VERSION  := $(shell cat VERSION)
DIST     := dist
HOSTARCH := $(shell $(GO) env GOARCH)
HOSTOS   := $(shell $(GO) env GOOS)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GOBUILD   = CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) $(GO) build -trimpath -ldflags "$(LDFLAGS)"

# Every built-in plugin is plugins/<name>/plugin.json + cmd/<name>.
PLUGINS  := $(notdir $(patsubst %/,%,$(dir $(wildcard plugins/*/plugin.json))))
# Example plugins carry code and manifest in examples/<name>/.
EXAMPLES := $(notdir $(patsubst %/,%,$(dir $(wildcard examples/*/plugin.json))))
SHELL_SCRIPTS := installer/install.sh installer/initd/rcplug examples/hello-sh/run.sh

LOCAL_ADDR  ?= 127.0.0.1:8080
LOCAL_MIXER ?= 127.0.0.1:2345
# Extra plugin directories for run-local, e.g. EXTRA_PLUGINS=../my-plugin
EXTRA_PLUGINS ?=

.PHONY: help all check fmt fmt-check vet lint test test-race test-arm64 fuzz web-test \
        build build-arm64 build-host examples package package-host run-local sim clean

help: ## show this help
	@grep -hE '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-15s %s\n", $$1, $$2}'

all: check package ## run all checks and build the release package

check: fmt-check vet test-race web-test lint ## everything CI runs (except arm64)

fmt: ## format the Go code
	gofmt -w .

fmt-check: ## fail if Go code is not formatted
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "not gofmt'ed:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

lint: ## shellcheck the shell scripts
	shellcheck $(SHELL_SCRIPTS)

test: ## unit tests
	$(GO) test ./...

test-race: ## unit tests with the race detector
	$(GO) test -race ./...

test-arm64: ## unit tests as arm64 binaries under qemu-user (needs qemu-aarch64)
	CGO_ENABLED=0 GOARCH=arm64 $(GO) test -exec qemu-aarch64 ./...

fuzz: ## fuzz the protocol decoder for 30 s
	$(GO) test -run '^$$' -fuzz FuzzDecodeChange -fuzztime 30s ./internal/rcproto

web-test: ## unit tests of the web UIs (needs Node 18+)
	node --test 'tools/webtest/*.test.mjs'

# ---- binaries: $(DIST)/<os>-<arch>/
build: build-arm64 build-host ## build all binaries for the device and this machine

build-arm64:
	@$(MAKE) --no-print-directory bin OS=linux ARCH=arm64

build-host:
	@$(MAKE) --no-print-directory bin OS=$(HOSTOS) ARCH=$(HOSTARCH)

.PHONY: bin
bin:
	@mkdir -p $(DIST)/$(OS)-$(ARCH)
	@for c in rcplugd rcsim rccapture $(PLUGINS); do echo "  $(OS)/$(ARCH) $$c"; \
	  $(GOBUILD) -o $(DIST)/$(OS)-$(ARCH)/$$c ./cmd/$$c || exit 1; done
	@for e in $(EXAMPLES); do [ -f examples/$$e/main.go ] || continue; echo "  $(OS)/$(ARCH) $$e (example)"; \
	  $(GOBUILD) -o $(DIST)/$(OS)-$(ARCH)/$$e ./examples/$$e || exit 1; done

examples: build-arm64 ## stage the example plugins for the device in dist/examples/
	@rm -rf $(DIST)/examples
	@$(call stage_examples,$(DIST)/examples)
	@echo "examples: $(DIST)/examples/  (install with the toolkit: rcplug-wizard.sh --host <ip> --plugin $(DIST)/examples/<name>)"

# Staging recipes are single shell commands (note the trailing backslashes).
# File modes are set explicitly; where the file system cannot store them (a
# network share, for example) the chmods fail silently, which is why the
# release tarballs are always packed from a temporary local directory.
fixmodes = find $(1) -type d -exec chmod 0755 {} + 2>/dev/null || true; \
	find $(1) -type f -exec chmod 0644 {} + 2>/dev/null || true

# $(call stage,<os>-<arch>,<dir>): the tree the toolkit pushes to the device
define stage
rm -rf $(2); mkdir -p $(2)/initd; \
cp $(DIST)/$(1)/rcplugd $(DIST)/$(1)/rccapture $(2)/; \
for p in $(PLUGINS); do mkdir -p $(2)/plugins/$$p; \
  cp $(DIST)/$(1)/$$p $(2)/plugins/$$p/$$p; cp plugins/$$p/plugin.json $(2)/plugins/$$p/; done; \
cp installer/install.sh $(2)/install.sh; cp installer/initd/rcplug $(2)/initd/rcplug; \
cp LICENSE THIRD_PARTY_NOTICES.md $(2)/; echo "$(VERSION)" > $(2)/VERSION; \
$(call fixmodes,$(2)); \
chmod 0755 $(2)/rcplugd $(2)/rccapture $(2)/install.sh $(2)/initd/rcplug \
  $(foreach p,$(PLUGINS),$(2)/plugins/$(p)/$(p)) 2>/dev/null || true
endef

# $(call stage_examples,<dir>): example plugins ready to copy (arm64 binaries)
define stage_examples
for e in $(EXAMPLES); do mkdir -p $(1)/$$e; cp examples/$$e/plugin.json $(1)/$$e/; \
  for f in examples/$$e/*.sh; do [ ! -f "$$f" ] || cp "$$f" $(1)/$$e/; done; \
  [ ! -f $(DIST)/linux-arm64/$$e ] || cp $(DIST)/linux-arm64/$$e $(1)/$$e/$$e; done; \
$(call fixmodes,$(1)); \
for f in $(1)/*/*.sh $(foreach e,$(EXAMPLES),$(1)/$(e)/$(e)); do [ ! -f "$$f" ] || chmod 0755 "$$f" 2>/dev/null || true; done
endef

package: build-arm64 ## release files in dist/: device tarball, examples tarball, SHA256SUMS
	@tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; set -e; \
	$(call stage,linux-arm64,$$tmp/payload); \
	$(call stage_examples,$$tmp/examples); \
	tar --owner=0 --group=0 -C "$$tmp/payload" -czf $(DIST)/rcplug-$(VERSION)-aarch64.tar.gz .; \
	tar --owner=0 --group=0 -C "$$tmp/examples" -czf $(DIST)/rcplug-examples-$(VERSION)-aarch64.tar.gz .; \
	rm -rf $(DIST)/examples; cp -r "$$tmp/examples" $(DIST)/examples; \
	cd $(DIST) && sha256sum rcplug-$(VERSION)-aarch64.tar.gz rcplug-examples-$(VERSION)-aarch64.tar.gz > SHA256SUMS
	@echo "packaged: $(DIST)/rcplug-$(VERSION)-aarch64.tar.gz (+ examples, SHA256SUMS)"

package-host: build-host ## payload for this machine in dist/payload-host (Linux only; for the toolkit's installer test)
	@$(call stage,$(HOSTOS)-$(HOSTARCH),$(DIST)/payload-host)

# ---- local development
run-local: build-host ## run the host with the console, the examples and EXTRA_PLUGINS against the simulator
	rm -rf $(DIST)/local/plugins
	@for p in $(PLUGINS); do mkdir -p $(DIST)/local/plugins/$$p; \
	  cp $(DIST)/$(HOSTOS)-$(HOSTARCH)/$$p $(DIST)/local/plugins/$$p/; cp plugins/$$p/plugin.json $(DIST)/local/plugins/$$p/; done
	@for e in $(EXAMPLES); do mkdir -p $(DIST)/local/plugins/$$e; cp examples/$$e/plugin.json $(DIST)/local/plugins/$$e/; \
	  if [ -f $(DIST)/$(HOSTOS)-$(HOSTARCH)/$$e ]; then cp $(DIST)/$(HOSTOS)-$(HOSTARCH)/$$e $(DIST)/local/plugins/$$e/; \
	  else cp examples/$$e/*.sh $(DIST)/local/plugins/$$e/; fi; done
	@for d in $(EXTRA_PLUGINS); do echo "  extra plugin $$d"; cp -r "$$d" $(DIST)/local/plugins/ || exit 1; done
	cp $(DIST)/$(HOSTOS)-$(HOSTARCH)/rcplugd $(DIST)/local/rcplugd
	cd $(DIST)/local && ( ../$(HOSTOS)-$(HOSTARCH)/rcsim --addr $(LOCAL_MIXER) & echo $$! > rcsim.pid ) && \
	  trap 'kill $$(cat rcsim.pid) 2>/dev/null' EXIT INT TERM && \
	  RCPLUG_ROOT=$$PWD RCPLUG_MIXER=$(LOCAL_MIXER) ./rcplugd --addr $(LOCAL_ADDR)

sim: build-host ## only the simulated desk
	$(DIST)/$(HOSTOS)-$(HOSTARCH)/rcsim --addr $(LOCAL_MIXER)

clean: ## remove dist/
	rm -rf $(DIST)
