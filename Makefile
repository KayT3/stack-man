# stack-man release builds.
#
#   make            # agent (Linux amd64 + arm64) and the desktop client
#   make agent      # cross-compiled Linux agent binaries -> dist/
#   make client     # Svelte bundle + Wails desktop app -> dist/
#   make frontend   # only the Vite bundle (needs Node/npm)
#   make check      # go build/vet/test across the three modules
#   make clean
#
# Toolchain notes:
#   * Go builds both artefacts.
#   * Node/npm is build-time only. Wails loads the Svelte UI through a webview,
#     so the frontend must be compiled to static files in client/frontend/dist
#     first; main_wails.go embeds that directory into the Go binary. The
#     released app neither ships nor runs Node. If you already have dist/,
#     `wails build -s` can skip the frontend step entirely.
#   * `make client` needs the Wails CLI. First time:
#       go install github.com/wailsapp/wails/v2/cmd/wails@latest
#     The Wails Go dependency is pinned in client/go.mod; `make wails-deps`
#     upgrades it.

SHELL := sh
.DEFAULT_GOAL := release

DIST         ?= dist
DIST_ABS     := $(abspath $(DIST))
GOFLAGS      ?= -trimpath
LDFLAGS      ?= -s -w
AGENT_TARGETS ?= linux/amd64 linux/arm64

HOST_OS   := $(shell go env GOOS)
HOST_ARCH := $(shell go env GOARCH)
EXE       := $(if $(filter windows,$(HOST_OS)),.exe)
DESKTOP   := stackman-desktop$(EXE)

.PHONY: release agent frontend client wails-deps check clean

release: agent client

agent:
	@mkdir -p "$(DIST)"
	@for target in $(AGENT_TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		out="$(DIST_ABS)/stackman-$$os-$$arch"; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go -C agent build $(GOFLAGS) -ldflags "$(LDFLAGS)" \
				-o "$$out" ./cmd/stackman || exit 1; \
	done

frontend:
	cd client/frontend && npm ci
	cd client/frontend && npm run build

client: frontend
	@command -v wails >/dev/null 2>&1 || { \
		echo "error: wails CLI not found"; \
		echo "install: go install github.com/wailsapp/wails/v2/cmd/wails@latest"; \
		echo "then:    make wails-deps"; \
		exit 1; \
	}
	cd client && wails build -tags wails -clean -trimpath -ldflags "$(LDFLAGS)" -s
	@mkdir -p "$(DIST)"
ifeq ($(HOST_OS),darwin)
	cp -R "client/build/bin/stackman-desktop.app" \
		"$(DIST)/stackman-desktop-$(HOST_OS)-$(HOST_ARCH).app"
else
	cp "client/build/bin/$(DESKTOP)" \
		"$(DIST)/stackman-desktop-$(HOST_OS)-$(HOST_ARCH)$(EXE)"
endif

wails-deps:
	cd client && go get github.com/wailsapp/wails/v2@latest

check:
	go build ./agent/... ./pkg/... ./client/...
	go vet ./agent/... ./pkg/... ./client/...
	go test ./agent/... ./pkg/... ./client/...

clean:
	rm -rf "$(DIST)" client/frontend/dist client/build/bin
