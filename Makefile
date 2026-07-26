BINARY = ru
MODULE = github.com/han/qrush

# Where `go install` drops the binary (GOBIN, else GOPATH/bin). This is the copy
# on your PATH — not the ./ru that `make build` produces in the project dir.
GOBIN = $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN = $(shell go env GOPATH)/bin
endif

.PHONY: build clean install uninstall test vet

build:
	go build -o $(BINARY) ./cmd/ru/
	@./$(BINARY) upgrade

clean:
	rm -f $(BINARY)

install:
	go install ./cmd/ru/
	@$(GOBIN)/$(BINARY) upgrade
	@echo
	@echo "Installed $(GOBIN)/$(BINARY)."
	@echo "The TUI runs client-side: fully QUIT any running '$(BINARY)' (top-level"
	@echo "window, not just detach) and relaunch to pick up this build. Running"
	@echo "'$(BINARY)' from inside a pane only re-surfaces the already-running TUI."

uninstall:
	rm -f $(shell go env GOPATH)/bin/$(BINARY)

test:
	go test -race ./...

vet:
	go vet ./...
