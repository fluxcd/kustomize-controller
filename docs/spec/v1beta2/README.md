# kustomize.toolkit.fluxcd.io/v1beta2

This is the v1beta1 API specification for defining continuous delivery pipelines
of Kubernetes objects generated with Kustomize.

## Specification

- [Kustomization CRD](kustomization.md)
    + [Source reference](kustomization.md#source-reference)
    + [Generate kustomization.yaml](kustomization.md#generate-kustomizationyaml)
    + [Reconciliation](kustomization.md#reconciliation)
    + [Garbage collection](kustomization.md#garbage-collection)
    + [Health assessment](kustomization.md#health-assessment)
    + [Kustomization dependencies](kustomization.md#kustomization-dependencies)
    + [Role-based access control](kustomization.md#role-based-access-control)
    + [Override kustomize config](kustomization.md#override-kustomize-config)
    + [Variable substitution](kustomization.md#variable-substitution)
    + [Targeting remote clusters](kustomization.md#remote-clusters--cluster-api)
    + [Secrets decryption](kustomization.md#secrets-decryption)
    + [Status](kustomization.md#status)

## Implementation

* [kustomize-controller](https://github.com/fluxcd/kustomize-controller/)
