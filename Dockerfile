# Build GoChain in a stock Go builder container
FROM golang:1.10-alpine as builder

RUN apk --no-cache add build-base git bzr mercurial gcc linux-headers
ENV D=/go/src/github.com/gochain-io/rpc-proxy
RUN go get -u github.com/golang/dep/cmd/dep
ADD Gopkg.* $D/
RUN cd $D && dep ensure --vendor-only
ADD . $D
RUN cd $D && go get && go build && cp rpc-proxy /tmp

# Pull all binaries into a second stage deploy alpine container
FROM alpine:latest
COPY --from=builder /tmp/rpc-proxy /usr/local/bin/
ENTRYPOINT ["rpc-proxy"]
