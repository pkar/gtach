GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w -X main.version=$(VERSION)

ifdef PREFIX
BINDIR ?= $(PREFIX)/bin
else
BINDIR ?= $(shell for d in /opt/homebrew/bin /usr/local/bin "$$HOME/.local/bin"; do \
 [ -w "$$d" ] && { echo "$$d"; exit; }; done; echo "$$HOME/.local/bin")
endif

.PHONY: all build test vet install uninstall clean
all: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o gtach ./cmd/gtach
test:
	$(GO) test ./...
vet:
	$(GO) vet ./...
install: build
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 gtach "$(DESTDIR)$(BINDIR)/gtach"
uninstall:
	rm -f "$(DESTDIR)$(BINDIR)/gtach"
clean:
	rm -f gtach
	rm -rf dist
