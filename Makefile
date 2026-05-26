BINARY_NAME=gitlab-search

.PHONY: all build clean test fmt help

all: build

build:
	go build -o $(BINARY_NAME) .

test:
	go test ./...

fmt:
	go fmt ./...

clean:
	rm -f $(BINARY_NAME)
	rm -f *.json

help:
	@echo "Available targets:"
	@echo "  build   - Build the binary"
	@echo "  test    - Run tests"
	@echo "  fmt     - Format the code"
	@echo "  clean   - Remove binary and generated json files"
	@echo "  help    - Show this help message"
