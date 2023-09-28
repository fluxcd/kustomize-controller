# kustomize.toolkit.fluxcd.io/v1

This is the v1 API specification for defining continuous delivery pipelines
of Kubernetes objects generated with Kustomize.

## Specification

- [Kustomization CRD](kustomizations.md)
    + [Example](kustomizations.md#example)
    + [Writing a Kustomization spec](kustomizations.md#writing-a-kustomization-spec)
    + [Working with Kustomizations](kustomizations.md#working-with-kustomizations)
      * [Recommended settings](kustomizations.md#recommended-settings)
    + [Kustomization Status](kustomizations.md#kustomization-status)

## Implementation

* [kustomize-controller](https://github.com/fluxcd/kustomize-controller/)
