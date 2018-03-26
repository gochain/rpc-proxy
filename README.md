# rpc-proxy

## Getting Started

### Prerequisites

Go Version 1.10+
if not installed you can follow this documentation https://golang.org/doc/install

## Deployment

You can run the program with the "port" and "url" sent as command line arguments. If not set by default rpc-proxy will be running on port 8545 and it will be redirecting the request to http://localhost:8080.

Make sure you run your program at 8545 port or specify port while running the program.

Run Commands:

If you want to host ReverseProxy on 8545 and redirect the request to the port 8040 , run following command

``` shell
./rpc-proxy -url http://SOME_IP:8545 -allow "eth_*" -rpm 100
```

or

``` shell
./rpc-proxy
```

## Output

When you run the rpc-proxy server, It will print the port where rpc-proxy is running and where it is redirecting the request to.

Whenever there is a http request , we are printing request body and response body along with headers.Along with that we are measuring the time for each api. Currently we are storing the total response time, total no of api calls for a particular path.

## Docker

Build Docker image:

```sh
make build
```

Run it:

```sh
docker run --rm -it -p 8545:8545 gochain/rpc-proxy -url http://SOME_IP:8545 -allow "eth_*,net_*" -rpm 1000
```
