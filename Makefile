.PHONY: build docker run install test

build:
	go build

run: build
	./rpc-proxy -url http://35.228.129.142/ -config config.toml

docker:
	docker build -t gochain/rpc-proxy .

# Proxy to the testnet node http://35.228.129.142/
run-docker: docker
	docker run --rm -it -p 8545:8545 -v ${PWD}/config.toml:/proxy.toml gochain/rpc-proxy -url http://35.228.129.142/ -port 8545 -rpm 1000 -config /proxy.toml -verbose

install:
	go install

test:
	go test ./...
