# kustomize-controller

[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/4787/badge)](https://bestpractices.coreinfrastructure.org/projects/4787)
[![e2e](https://github.com/fluxcd/kustomize-controller/workflows/e2e/badge.svg)](https://github.com/fluxcd/kustomize-controller/actions)
[![report](https://goreportcard.com/badge/github.com/fluxcd/kustomize-controller)](https://goreportcard.com/report/github.com/fluxcd/kustomize-controller)
[![license](https://img.shields.io/github/license/fluxcd/kustomize-controller.svg)](https://github.com/fluxcd/kustomize-controller/blob/main/LICENSE)
[![release](https://img.shields.io/github/release/fluxcd/kustomize-controller/all.svg)](https://github.com/fluxcd/kustomize-controller/releases)

The kustomize-controller is a [Flux](https://github.com/fluxcd/flux2) component,
specialized in running continuous delivery pipelines for infrastructure and workloads
defined with Kubernetes manifests and assembled with Kustomize.

The cluster desired state is described through a Kubernetes Custom Resource named `Kustomization`.
Based on the creation, mutation or removal of a `Kustomization` resource in the cluster,
the controller performs actions to reconcile the cluster current state with the desired state.

![overview](docs/diagrams/kustomize-controller-overview.png)

## Features

* watches for `Kustomization` objects
* fetches artifacts produced by [source-controller](https://github.com/fluxcd/source-controller) from `Source` objects 
* watches `Source` objects for revision changes 
* generates the `kustomization.yaml` file if needed
* generates Kubernetes manifests with Kustomize SDK
* decrypts Kubernetes secrets with Mozilla SOPS and KMS
* validates the generated manifests with Kubernetes server-side apply dry-run
- detects drift between the desired and state and cluster state
- corrects drift by patching objects with Kubernetes server-side apply
* prunes the Kubernetes objects removed from source
* checks the health of the deployed workloads
* runs `Kustomizations` in a specific order, taking into account the depends-on relationship 
* notifies whenever a `Kustomization` status changes

## Specifications

* [API](docs/spec/v1/README.md)
* [Controller](docs/spec/README.md)

## Guides

* [Get started with Flux](https://fluxcd.io/flux/get-started/)
* [Setup Notifications](https://fluxcd.io/flux/guides/notifications/)
* [Manage Kubernetes secrets with Flux and SOPS](https://fluxcd.io/flux/guides/mozilla-sops/)
* [How to build, publish and consume OCI Artifacts with Flux](https://fluxcd.io/flux/cheatsheets/oci-artifacts/)
* [Flux and Kustomize FAQ](https://fluxcd.io/flux/faq/#kustomize-questions)

## Roadmap

The roadmap for the Flux family of projects can be found at <https://fluxcd.io/roadmap/>.

## Contributing

This project is Apache 2.0 licensed and accepts contributions via GitHub pull requests.
To start contributing please see the [development guide](DEVELOPMENT.md).
