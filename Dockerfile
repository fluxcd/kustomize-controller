ARG GO_VERSION=1.24
ARG XX_VERSION=1.6.1

FROM --platform=$BUILDPLATFORM tonistiigi/xx:${XX_VERSION} AS xx

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

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
COPY internal/ internal/

# build
ENV CGO_ENABLED=0
RUN xx-go build -trimpath -a -o kustomize-controller main.go

FROM alpine:3.21

ARG TARGETPLATFORM

RUN apk --no-cache add ca-certificates tini git openssh-client>=9.6_p1-r2 gnupg \
  && update-ca-certificates

COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

USER 65534:65534

ENV GNUPGHOME=/tmp

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
