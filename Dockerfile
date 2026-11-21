# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fn-media-proxy ./cmd/fn-media-proxy

# ---- runtime stage ----
# NOTE: do NOT run `apk add` here. Accessing dl-cdn.alpinelinux.org is very slow
# or times out from mainland China, which stalls the build for many minutes.
# CA certs are copied from the build image (needed for HTTPS direct links).
# Timezone data is intentionally skipped: alpine base has no /usr/share/zoneinfo
# and TZ only affects log timestamps in this service.
FROM alpine:3.20
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/fn-media-proxy /usr/local/bin/fn-media-proxy
EXPOSE 8096
ENTRYPOINT ["/usr/local/bin/fn-media-proxy"]
