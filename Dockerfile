
# Pull all binaries into a second stage deploy alpine container
FROM alpine:latest
COPY rpc-proxy /usr/local/bin/
CMD ["rpc-proxy"]
