# Build GoChain in a stock Go builder container
FROM golang:1.12-alpine as builder

RUN apk --no-cache add build-base git bzr mercurial gcc linux-headers
ENV D=/rpc-proxy
WORKDIR $D
# cache dependencies
ADD go.mod $D
ADD go.sum $D
RUN go mod download
# build
ADD . $D
RUN cd $D && go build && cp rpc-proxy /tmp

# Pull all binaries into a second stage deploy alpine container
FROM alpine:latest
COPY --from=builder /tmp/rpc-proxy /usr/local/bin/
ENTRYPOINT ["rpc-proxy"]
