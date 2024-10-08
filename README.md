# rpc-proxy

A proxy for `web3` JSONRPC featuring:

- rate limiting
- method filtering
- stats

## Getting Started

### Prerequisites

At least Go 1.12. Installation documentation here: https://golang.org/doc/install

### How to Use

By default, `rpc-proxy` will run on port `8545` and redirect requests to `http://localhost:8040`. These values
can be changed with the `port` and `url` flags, along with other options:

```sh
> rpc-proxy help
NAME:
   rpc-proxy - A proxy for web3 JSONRPC

USAGE:
   rpc-proxy [global options] command [command options] [arguments...]

VERSION:
   0.0.28

COMMANDS:
     help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   --config value, -c value   path to toml config file
   --port value, -p value     port to serve (default: "8545")
   --url value, -u value      redirect url (default: "http://127.0.0.1:8040")
   --allow value, -a value    comma separated list of allowed paths
   --rpm value                limit for number of requests per minute from single IP (default: 1000)
   --nolimit value, -n value  list of ips allowed unlimited requests(separated by commas)
   --verbose                  verbose logging enabled
   --help, -h                 show help
   --version, -v              print the version
```

## Docker

Build Docker image:

```sh
make docker
```

Run it:

```sh
make run
```
