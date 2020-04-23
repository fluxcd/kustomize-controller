# Changelog

All notable changes to this project are documented in this file.

## 0.0.1-alpha.3 (2020-04-23)

This alpha release introduces the option to tell the controller to
[automatically generate](https://github.com/fluxcd/kustomize-controller/blob/master/docs/spec/v1alpha1/kustomization.md#generate-kustomizationyaml)
the `kustomization.yaml` for repositories that contain plain Kubernetes manifests.

The controller design and motivation can be found at
[docs/spec](https://github.com/fluxcd/kustomize-controller/tree/master/docs/sp).

## 0.0.1-alpha.2 (2020-04-21)

This alpha release introduces the
[Profile CRD](https://github.com/fluxcd/kustomize-controller/blob/master/docs/spec/v1alpha1/profile.md)
that allows grouping
[Kustomization](https://github.com/fluxcd/kustomize-controller/blob/master/docs/spec/v1alpha1/kustomization.md)
objects and defining a common behavior for them.
The v1alpha1 profiles can be used for
[configuring Slack and Discord alerting](https://github.com/fluxcd/kustomize-controller/tree/master#configure-alerting).

## 0.0.1-alpha.1 (2020-04-20)

This is the first alpha release of kustomize controller.
The controller is an implementation of the
[kustomize.fluxcd.io/v1alpha1](https://github.com/fluxcd/kustomize-controller/tree/master/docs/spec/v1alpha1) API.
