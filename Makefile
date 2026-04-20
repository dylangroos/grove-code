.PHONY: build install uninstall

BIN := grove
PKG := ./cmd/grove

build:
	go build -o $(BIN) $(PKG)

install:
	go install $(PKG)

uninstall:
	rm -f $(shell go env GOPATH)/bin/$(BIN)
