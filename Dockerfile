FROM golang:1.16-alpine as builder

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
RUN CGO_ENABLED=0 go build -a -o kustomize-controller main.go

FROM alpine:3.14

LABEL org.opencontainers.image.source="https://github.com/fluxcd/kustomize-controller"

RUN apk add --no-cache ca-certificates tini git openssh-client gnupg

COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

# Create minimal nsswitch.conf file to prioritize the usage of /etc/hosts over DNS queries.
# https://github.com/gliderlabs/docker-alpine/issues/367#issuecomment-354316460
RUN [ ! -e /etc/nsswitch.conf ] && echo 'hosts: files dns' > /etc/nsswitch.conf

RUN addgroup -S controller && adduser -S controller -G controller

USER controller

ENV GNUPGHOME=/tmp

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
