.PHONY: build docker release install test

build:
	go build

docker:
	docker build -t gochain/rpc-proxy .

release:
	./release.sh

install:
	go install

test:
	go test ./...
