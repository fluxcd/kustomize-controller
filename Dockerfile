FROM golang:1.16-alpine as builder

ARG TARGETPLATFORM

WORKDIR /workspace

RUN apk add --no-cache ca-certificates curl

RUN kubectl_ver=1.21.2 && \
arch=${TARGETPLATFORM:-linux/amd64} && \
if [ "$TARGETPLATFORM" == "linux/arm/v7" ]; then arch="linux/arm"; fi && \
curl -sL https://storage.googleapis.com/kubernetes-release/release/v${kubectl_ver}/bin/${arch}/kubectl \
-o /usr/local/bin/kubectl && chmod +x /usr/local/bin/kubectl

RUN kubectl version --client=true

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

COPY --from=builder /usr/local/bin/kubectl /usr/local/bin/
COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

# Create minimal nsswitch.conf file to prioritize the usage of /etc/hosts over DNS queries.
# https://github.com/gliderlabs/docker-alpine/issues/367#issuecomment-354316460
RUN [ ! -e /etc/nsswitch.conf ] && echo 'hosts: files dns' > /etc/nsswitch.conf

RUN addgroup -S controller && adduser -S controller -G controller

USER controller

ENV GNUPGHOME=/tmp
COPY config/kubeconfig /home/controller/.kube/config

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
