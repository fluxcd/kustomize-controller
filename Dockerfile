FROM golang:1.14 as builder

WORKDIR /workspace

RUN kubectl_ver=1.19.0 && \
curl -sL https://storage.googleapis.com/kubernetes-release/release/v${kubectl_ver}/bin/linux/amd64/kubectl \
-o /usr/local/bin/kubectl && chmod +x /usr/local/bin/kubectl

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

# build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o kustomize-controller main.go

FROM alpine:3.12

RUN apk add --no-cache ca-certificates tini git gnupg

COPY --from=builder /usr/local/bin/kubectl /usr/local/bin/
COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

# Create minimal nsswitch.conf file to prioritize the usage of /etc/hosts over DNS queries.
# https://github.com/gliderlabs/docker-alpine/issues/367#issuecomment-354316460
RUN [ ! -e /etc/nsswitch.conf ] && echo 'hosts: files dns' > /etc/nsswitch.conf

RUN addgroup -S controller && adduser -S -g controller controller

USER controller

ENV GNUPGHOME=/tmp
COPY config/kubeconfig /home/controller/.kube/config

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
