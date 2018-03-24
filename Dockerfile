# Build GoChain in a stock Go builder container
FROM golang:1.10-alpine as builder

RUN apk --no-cache add build-base git bzr mercurial gcc linux-headers
ENV D=/go/src/github.com/gochain-io/rpc-proxy
ADD . $D
RUN cd $D && go get && go build && cp rpc-proxy /tmp

# Pull all binaries into a second stage deploy alpine container
FROM alpine:latest
# COPY rpc-proxy /usr/local/bin/
COPY --from=builder /tmp/rpc-proxy /usr/local/bin/
CMD ["rpc-proxy"]
