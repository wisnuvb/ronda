VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test vuln build demo build-linux build-arm64 release check-host deploy clean

test:
	go vet ./...
	go test -race ./...

# Known-vulnerability scan of the code and of the Go standard library it is built with. Needs network.
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Try the dashboard with synthetic data and no login; edit web/static and refresh to see changes.
demo:
	go run ./cmd/ronda demo -static web/static

# Local binary for this machine: bin/ronda
build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ronda ./cmd/ronda

# Static Linux binaries for a server.
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ronda-linux-amd64 ./cmd/ronda

build-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ronda-linux-arm64 ./cmd/ronda

# One tarball a server needs: both binaries, the installer, the unit, and examples.
release: test build-linux build-arm64
	rm -rf dist/ronda-$(VERSION) && mkdir -p dist/ronda-$(VERSION)
	cp bin/ronda-linux-amd64 bin/ronda-linux-arm64 dist/ronda-$(VERSION)/
	cp deploy/install.sh deploy/ronda.service deploy/config.example.json deploy/nginx.conf.example README.md dist/ronda-$(VERSION)/
	tar -C dist -czf dist/ronda-$(VERSION).tar.gz ronda-$(VERSION)
	@echo "dist/ronda-$(VERSION).tar.gz"

# Read-only: can HOST run ronda?   make check-host HOST=ubuntu@1.2.3.4
check-host: release
	@test -n "$(HOST)" || { echo "usage: make check-host HOST=user@server"; exit 1; }
	scp dist/ronda-$(VERSION).tar.gz $(HOST):/tmp/
	ssh $(HOST) 'cd /tmp && tar xzf ronda-$(VERSION).tar.gz && cd ronda-$(VERSION) && ./install.sh --check'

# Ship and install in one go.   make deploy HOST=user@server ARGS="--set-password"
deploy: release
	@test -n "$(HOST)" || { echo "usage: make deploy HOST=user@server [ARGS=...]"; exit 1; }
	scp dist/ronda-$(VERSION).tar.gz $(HOST):/tmp/
	ssh -t $(HOST) 'cd /tmp && tar xzf ronda-$(VERSION).tar.gz && cd ronda-$(VERSION) && sudo ./install.sh $(ARGS)'

clean:
	rm -rf bin data dist
