FROM golang:1.13 as builder

WORKDIR /workspace

RUN kustomize_ver=3.6.1 && \
kustomize_url=https://github.com/kubernetes-sigs/kustomize/releases/download && \
curl -sL ${kustomize_url}/kustomize%2Fv${kustomize_ver}/kustomize_v${kustomize_ver}_linux_amd64.tar.gz | \
tar xz && mv kustomize /usr/local/bin/kustomize

RUN kubectl_ver=1.18.3 && \
curl -sL https://storage.googleapis.com/kubernetes-release/release/v${kubectl_ver}/bin/linux/amd64/kubectl \
-o /usr/local/bin/kubectl && chmod +x /usr/local/bin/kubectl

# copy modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache modules
RUN go mod download

# copy source code
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY internal/ internal/

# build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o kustomize-controller main.go

FROM alpine:3.12

RUN apk add --no-cache ca-certificates tar tini git socat curl bash

COPY --from=builder /usr/local/bin/kustomize /usr/local/bin/
COPY --from=builder /usr/local/bin/kubectl /usr/local/bin/
COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

RUN addgroup -S controller && adduser -S -g controller controller

USER controller

COPY config/kubeconfig /home/controller/.kube/config

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
