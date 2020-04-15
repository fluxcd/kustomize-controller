#!/bin/sh -l

VERSION=3.1.0
curl -sfLo kustomize https://github.com/kubernetes-sigs/kustomize/releases/download/v${VERSION}/kustomize_${VERSION}_linux_amd64

mkdir -p $GITHUB_WORKSPACE/bin
cp ./kustomize $GITHUB_WORKSPACE/bin
chmod +x $GITHUB_WORKSPACE/bin/kustomize
ls -lh $GITHUB_WORKSPACE/bin

echo "::add-path::$GITHUB_WORKSPACE/bin"
echo "::add-path::$RUNNER_WORKSPACE/$(basename $GITHUB_REPOSITORY)/bin"
