APP := vortex-agent
PKG := ./cmd/vortex-agent
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X vortex-agent/internal/version.Version=$(VERSION)

.PHONY: build test vet clean install cross cross-linux publish-web

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(APP) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin/

install: build
	install -Dm755 bin/$(APP) /usr/local/bin/$(APP)
	install -Dm644 deploy/vortex-agent.service /etc/systemd/system/vortex-agent.service
	@echo "Copy .env.example to /etc/vortex-agent.env and systemctl enable --now vortex-agent"

# Cross-compile common server targets (static-ish, CGO disabled).
cross-linux:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP)-linux-arm64 $(PKG)

cross: cross-linux
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP)-darwin-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP)-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP)-windows-amd64.exe $(PKG)

# Copy Linux agents into Vortex Web static dir (local/dev only).
# Production: publish a GitHub Release — Web image downloads assets at build time.
WEB_PUBLIC ?= ../VortexWeb/public/agent
publish-web: cross-linux
	mkdir -p "$(WEB_PUBLIC)"
	cp -f bin/$(APP)-linux-amd64 bin/$(APP)-linux-arm64 "$(WEB_PUBLIC)/"
	@echo "Published to $(WEB_PUBLIC)"

# Example release (needs gh auth):
#   make cross-linux
#   gh release create v0.1.0 bin/vortex-agent-linux-amd64 bin/vortex-agent-linux-arm64
