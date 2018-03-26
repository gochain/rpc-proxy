.PHONY: dep build docker release install test

dep:
	dep ensure --vendor-only

build:
	go build

docker:
	docker build -t gochain/rpc-proxy .

release:
	./release.sh

install: build
	cp bin/gochain-bootnode $(GOPATH)/bin/gochain-bootnode
	cp bin/gochain $(GOPATH)/bin/gochain

test:
	go test ./...
