ARG GO_VERSION=1.19
ARG XX_VERSION=1.1.0

FROM --platform=$BUILDPLATFORM tonistiigi/xx:${XX_VERSION} AS xx

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine as builder

# Copy the build utilities.
COPY --from=xx / /

ARG TARGETPLATFORM

WORKDIR /workspace

# copy api submodule
COPY api/ api/

# copy modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache modules
RUN go mod download

# copy source code
COPY main.go main.go
COPY controllers/ controllers/
COPY internal/ internal/

# build
ENV CGO_ENABLED=0
RUN xx-go build -trimpath -a -o kustomize-controller main.go

FROM alpine:3.18

# Uses GnuPG from edge to patch CVE-2022-3515.
RUN apk add --no-cache ca-certificates tini git openssh-client && \
	apk add --no-cache gnupg --repository=https://dl-cdn.alpinelinux.org/alpine/edge/main

COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

USER 65534:65534

ENV GNUPGHOME=/tmp

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
