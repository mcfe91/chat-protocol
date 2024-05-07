run: build
	@./bin/tcp-protocol

build:
	@go build -o bin/tcp-protocol .