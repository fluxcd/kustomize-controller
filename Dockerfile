# go mod download cache stage

FROM --platform=$BUILDPLATFORM golang:1.15-alpine as gomod

WORKDIR /workspace

# copy go modules manifests
COPY ./api/go.mod ./api/go.sum ./api/
COPY go.mod go.sum ./

# download dependencies
RUN go mod download

# ------------------------------------------------------------------------------
# go crossbuild stage

FROM --platform=$BUILDPLATFORM golang:1.15-alpine as builder

ARG TARGETPLATFORM

WORKDIR /workspace

COPY --from=gomod /go/pkg/ /go/pkg/

# copy sources
COPY . .

# build
RUN CGO_ENABLED=0 \
GOOS=$(echo "$TARGETPLATFORM" | cut -d '/' -f1 ) \
GOARCH=$(echo "$TARGETPLATFORM" | cut -d '/' -f2 ) \
GOARM=$(echo "$TARGETPLATFORM" | cut -d '/' -f3 | sed "s/^v//") \
go build -a -trimpath -o kustomize-controller main.go

# ------------------------------------------------------------------------------
# Final images build stage

FROM --platform=$TARGETPLATFORM alpine:3.13

ARG TARGETPLATFORM

LABEL org.opencontainers.image.source="https://github.com/fluxcd/kustomize-controller"

RUN apk add --no-cache ca-certificates curl tini git openssh-client gnupg

RUN kubectl_ver=1.20.2 && \
arch=${TARGETPLATFORM:-linux/amd64} && \
if [ "$TARGETPLATFORM" == "linux/arm/v7" ]; then arch="linux/arm"; fi && \
curl -sL https://storage.googleapis.com/kubernetes-release/release/v${kubectl_ver}/bin/${arch}/kubectl \
-o /usr/local/bin/kubectl && chmod +x /usr/local/bin/kubectl

RUN kubectl version --client=true

COPY --from=builder /workspace/kustomize-controller /usr/local/bin/

# Create minimal nsswitch.conf file to prioritize the usage of /etc/hosts over DNS queries.
# https://github.com/gliderlabs/docker-alpine/issues/367#issuecomment-354316460
RUN [ ! -e /etc/nsswitch.conf ] && echo 'hosts: files dns' > /etc/nsswitch.conf

RUN addgroup -S controller && adduser -S -g controller controller

USER controller

ENV GNUPGHOME=/tmp
COPY config/kubeconfig /home/controller/.kube/config

ENTRYPOINT [ "/sbin/tini", "--", "kustomize-controller" ]
