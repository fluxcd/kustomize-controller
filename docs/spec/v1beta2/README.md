# kustomize.toolkit.fluxcd.io/v1beta2

This is the v1beta2 API specification for defining continuous delivery pipelines
of Kubernetes objects generated with Kustomize.

## Specification

- [Kustomization CRD](kustomizations.md)
    + [Example](kustomizations.md#example)
    + [Recommended settings](kustomizations.md#recommended-settings)
    + [Source reference](kustomizations.md#source-reference)
    + [Generate kustomization.yaml](kustomizations.md#generate-kustomizationyaml)
    + [Reconciliation](kustomizations.md#reconciliation)
    + [Garbage collection](kustomizations.md#garbage-collection)
    + [Health assessment](kustomizations.md#health-assessment)
    + [Kustomization dependencies](kustomizations.md#kustomization-dependencies)
    + [Role-based access control](kustomizations.md#role-based-access-control)
    + [Override kustomize config](kustomizations.md#override-kustomize-config)
    + [Variable substitution](kustomizations.md#variable-substitution)
    + [Targeting remote clusters](kustomizations.md#remote-clusters--cluster-api)
    + [Secrets decryption](kustomizations.md#secrets-decryption)
    + [Status](kustomizations.md#status)

## Implementation

* [kustomize-controller](https://github.com/fluxcd/kustomize-controller/)
