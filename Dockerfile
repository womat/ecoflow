# Image for ecoflowd: the static binary, the CA bundle it needs for TLS to the
# EcoFlow cloud, and nothing else - no shell, no package manager, no state on
# disk. The same stance as the systemd unit, which runs it under DynamicUser.
#
# The build stage runs on the build machine's own platform and cross-compiles,
# so a multi-arch build needs no emulation:
#
#   docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/arm/v6 .

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS TARGETARCH TARGETVARIANT
# The release passes the tag; a local build says "dev".
ARG VERSION=dev
# GOARM comes from the variant (v6, v7); Go ignores it on every other arch.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /ecoflowd ./cmd/ecoflowd

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /ecoflowd /ecoflowd
# 65532 is the conventional "nonroot" id; the process needs no account, it
# only needs not to be root.
USER 65532:65532
ENTRYPOINT ["/ecoflowd"]
