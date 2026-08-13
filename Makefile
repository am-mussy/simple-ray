VERSION ?= 0.1.0
GO ?= go
LDFLAGS := -s -w -X github.com/mussy/simple-ray/internal/domain.ProductVersion=$(VERSION)

.PHONY: test vet build release clean

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/vpnctl ./cmd/vpnctl

release:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/vpnctl_$(VERSION)_linux_amd64/vpnctl ./cmd/vpnctl
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/vpnctl_$(VERSION)_linux_arm64/vpnctl ./cmd/vpnctl

clean:
	$(GO) clean
