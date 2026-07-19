BINARY = ru
MODULE = github.com/han/qrush

.PHONY: build clean install uninstall test vet

build:
	go build -o $(BINARY) ./cmd/ru/
	@./$(BINARY) upgrade

clean:
	rm -f $(BINARY)

install:
	go install ./cmd/ru/

uninstall:
	rm -f $(shell go env GOPATH)/bin/$(BINARY)

test:
	go test -race ./...

vet:
	go vet ./...
