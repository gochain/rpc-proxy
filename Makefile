.PHONY: build docker install test

build:
	go build

docker:
	docker build -t gochain/rpc-proxy .

install:
	go install

test:
	go test ./...
