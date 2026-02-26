# Changelog

All notable changes to this project are documented in this file.

## 1.8.1

**Release date:** 2026-02-27

This patch release fixes health check logic for StatefulSets during
rolling updates when the Pods are Pending/Unschedulable.

Fixes:
- Fix health check logic for StatefulSets during rolling updates
  [#1602](https://github.com/fluxcd/kustomize-controller/pull/1602)

Improvements:
- Improve docs for `.spec.force`
  [#1597](https://github.com/fluxcd/kustomize-controller/pull/1597)
- Remove no longer needed workaround for Flux 2.8
  [#1595](https://github.com/fluxcd/kustomize-controller/pull/1595)

## 1.8.0

**Release date:** 2026-02-17

This minor release comes with various bug fixes and improvements.

⚠️ The `v1beta2` APIs were removed. Before upgrading the CRDs, Flux users
must run [`flux migrate`](https://github.com/fluxcd/flux2/pull/5473) to
migrate the cluster storage off `v1beta2`.

### Kustomization

The controller now cancels in-progress health checks when a new reconciliation
request is received, reducing the mean time to recovery (MTTR) in case of
failed deployments. This is available through the `CancelHealthCheckOnNewRevision`
feature gate, that previously worked only for new source revisions but now also
works for any watch events that trigger a new reconciliation.

A custom SSA stage has been introduced, allowing Role and RoleBinding objects
to be applied in the same call even when the impersonated ServiceAccount does
not have a ClusterRoleBinding for `cluster-admin`. This can be specified with
the flag `--custom-apply-stage-kinds=rbac.authorization.k8s.io/Role`.

Health checks now handle Jobs with TTL set to zero seconds that are deleted
before or during health checking.

A `DirectSourceFetch` feature gate has been added to bypass cache for source
objects, enabling immediate consistency for source object reads.

### General updates

In addition, the Kubernetes dependencies have been updated to v1.35.0,
Kustomize has been updated to v5.8.1 and the controller is now built
with Go 1.26.

Fixes:
- Fix decryptor copy of auth.Option slices (avoid overrides)
  [#1570](https://github.com/fluxcd/kustomize-controller/pull/1570)

Improvements:
- Reduce the mean time to recovery (MTTR) in case of failed deployments
  [#1536](https://github.com/fluxcd/kustomize-controller/pull/1536)
- Introduce custom SSA stage
  [#1571](https://github.com/fluxcd/kustomize-controller/pull/1571)
- Handle Jobs with TTL in health checks
  [#1578](https://github.com/fluxcd/kustomize-controller/pull/1578)
- Add `DirectSourceFetch` feature gate to bypass cache for source objects
  [#1586](https://github.com/fluxcd/kustomize-controller/pull/1586)
- Remove deprecated APIs in group `kustomize.toolkit.fluxcd.io/v1beta2`
  [#1584](https://github.com/fluxcd/kustomize-controller/pull/1584)
- Various dependency updates
  [#1566](https://github.com/fluxcd/kustomize-controller/pull/1566)
  [#1572](https://github.com/fluxcd/kustomize-controller/pull/1572)
  [#1575](https://github.com/fluxcd/kustomize-controller/pull/1575)
  [#1581](https://github.com/fluxcd/kustomize-controller/pull/1581)
  [#1585](https://github.com/fluxcd/kustomize-controller/pull/1585)
  [#1588](https://github.com/fluxcd/kustomize-controller/pull/1588)
  [#1589](https://github.com/fluxcd/kustomize-controller/pull/1589)

## 1.7.3

**Release date:** 2025-11-19

This patch release comes with a series of bug fixes, including
Azure Workload Identity in Azure China Cloud. It also adds a
feature gate to disable the ConfigMap and Secret watchers,
`DisableConfigWatchers`.

Fixes:
- Fix: skip trying to decrypt remote patches as local
  [#1548](https://github.com/fluxcd/kustomize-controller/pull/1548)
- Add GVK to error message
  [#1547](https://github.com/fluxcd/kustomize-controller/pull/1547)

Improvements:
- Add feature gate for disabling config watchers
  [#1553](https://github.com/fluxcd/kustomize-controller/pull/1553)
- Upgrade k8s to 1.34.2 and c-r to 0.22.4
  [#1551](https://github.com/fluxcd/kustomize-controller/pull/1551)

## 1.7.2

**Release date:** 2025-10-28

This patch release allows running kustomize-controller
on the same loopback interface as source-watcher.

Improvements:
- Allow fetching artifacts from a local source-watcher
  [#1539](https://github.com/fluxcd/kustomize-controller/pull/1539)

## 1.7.1

**Release date:** 2025-10-08

This patch release comes with various dependency updates.

The controller is now built with Go 1.25.2 which includes
fixes for vulnerabilities in the Go stdlib:
[CVE-2025-58183](https://github.com/golang/go/issues/75677),
[CVE-2025-58188](https://github.com/golang/go/issues/75675)
and many others. The full list of security fixes can be found
[here](https://groups.google.com/g/golang-announce/c/4Emdl2iQ_bI/m/qZN5nc-mBgAJ).

Improvements:
- Update dependencies to Kubernetes v1.34.1 and Go 1.25.2
  [#1530](https://github.com/fluxcd/kustomize-controller/pull/1530)

## 1.7.0

**Release date:** 2025-09-24

This minor release comes with various bug fixes and improvements.

⚠️ The `v1beta1` APIs were removed. Before upgrading the CRDs, Flux users
must run [`flux migrate`](https://github.com/fluxcd/flux2/pull/5473) to
migrate the cluster storage off `v1beta1`.

The Kustomization API now supports reconciling content from external storage systems
through the new `ExternalArtifact` CRD. This feature is controlled by the `ExternalArtifact`
feature gate and enables reconciliation of artifacts stored outside of Git repositories.

Kustomizations now track their reconciliation history in `.status.history`, providing
visibility into past reconciliation attempts and their outcomes.

Dependencies can now be evaluated using CEL expressions through the new `readyExpr` field
in `dependsOn`. This feature is controlled by the `AdditiveCELDependencyCheck` feature gate
and allows for more flexible dependency readiness checks.

The controller now supports global SOPS Age key decryption, allowing centralized
management of decryption keys.

Support for workload identity authentication has been added for remote clusters.
This is support both at the controller and object levels. For object-level,
enable the feature gate `ObjectLevelWorkloadIdentity`.

The new `.spec.ignoreMissingComponents` field allows Kustomizations to continue
reconciliation even when referenced components are missing, providing more resilient
deployments.

A feature gate `CancelHealthCheckOnNewRevision` has been added to cancel ongoing
health checks when a new revision is detected.

In addition, the Kubernetes dependencies have been updated to v1.34,
Kustomize has been updated to v5.7 and various other controller
dependencies have been updated to their latest version.
The controller is now built with Go 1.25.

Fixes:
- Fix skipped entries from SSA being stored in the inventory
  [#1513](https://github.com/fluxcd/kustomize-controller/pull/1513)

Improvements:
- [RFC-0010] Add workload identity authentication for remote clusters
  [#1476](https://github.com/fluxcd/kustomize-controller/pull/1476)
- [RFC-0010] Add multi-tenancy lockdown for decryption and kubeconfig
  [#1495](https://github.com/fluxcd/kustomize-controller/pull/1495)
- [RFC-0010] Add object-level configuration validation
  [#1497](https://github.com/fluxcd/kustomize-controller/pull/1497)
- [RFC-0010] Add support for all Azure clouds for remote cluster authentication
  [#1488](https://github.com/fluxcd/kustomize-controller/pull/1488)
- [RFC-0012] Add support for ExternalArtifact source type
  [#1508](https://github.com/fluxcd/kustomize-controller/pull/1508)
- Add reconciliation history tracking in Kustomization status
  [#1502](https://github.com/fluxcd/kustomize-controller/pull/1502)
- Add CEL expressions for dependency readiness evaluation
  [#1491](https://github.com/fluxcd/kustomize-controller/pull/1491)
- Add `.spec.ignoreMissingComponents` field to Kustomization API
  [#1507](https://github.com/fluxcd/kustomize-controller/pull/1507)
- Add global SOPS Age key decryption support
  [#1481](https://github.com/fluxcd/kustomize-controller/pull/1481)
- Add label selector for watching ConfigMaps and Secrets
  [#1486](https://github.com/fluxcd/kustomize-controller/pull/1486)
- Add feature gate to cancel health checks on new revisions
  [#1520](https://github.com/fluxcd/kustomize-controller/pull/1520)
- Refactor reconciler to use staged server-side apply
  [#1484](https://github.com/fluxcd/kustomize-controller/pull/1484)
- Remove deprecated APIs in group `kustomize.toolkit.fluxcd.io/v1beta1`
  [#1494](https://github.com/fluxcd/kustomize-controller/pull/1494)
- CI improvements with `fluxcd/gha-workflows`
  [#1512](https://github.com/fluxcd/kustomize-controller/pull/1512)
  [#1514](https://github.com/fluxcd/kustomize-controller/pull/1514)
- Various dependency updates
  [#1470](https://github.com/fluxcd/kustomize-controller/pull/1470)
  [#1505](https://github.com/fluxcd/kustomize-controller/pull/1505)
  [#1511](https://github.com/fluxcd/kustomize-controller/pull/1511)
  [#1515](https://github.com/fluxcd/kustomize-controller/pull/1515)

## 1.6.1

**Release date:** 2025-07-08

This patch release fixes a bug introduced in v1.6.0
that causes SOPS decryption with US Government KMS
keys to fail with the error:

```
STS: AssumeRoleWithWebIdentity, https response error\n   StatusCode: 0, RequestID: ,
request send failed, Post\n \"https://sts.arn.amazonaws.com/\": dial tcp:
lookupts.arn.amazonaws.com on 10.100.0.10:53: no such host
```

Fixes:
- Fix regression in STS endpoint for SOPS decryption with AWS KMS in US Gov partition
  [#1478](https://github.com/fluxcd/kustomize-controller/pull/1478)

## 1.6.0

**Release date:** 2025-05-28

This minor release comes with various bug fixes and improvements.

Kustomization API now supports object-level workload identity by setting
`.spec.decryption.serviceAccountName` to the name of a service account
in the same namespace that has been configured with appropriate cloud
permissions. For this feature to work, the controller feature gate
`ObjectLevelWorkloadIdentity` must be enabled. See a complete guide
[here](https://fluxcd.io/flux/integrations/).

Kustomization API now supports the value `WaitForTermination` for the
`.spec.deletionPolicy` field. This instructs the controller to wait for the
deletion of all resources managed by the Kustomization before allowing the
Kustomization itself to be deleted. See docs
[here](https://fluxcd.io/flux/components/kustomize/kustomizations/#deletion-policy).

In addition, the Kubernetes dependencies have been updated to v1.33 and
various other controller dependencies have been updated to their latest version.
The controller is now built with Go 1.24.

Fixes:
- Fix performance regression due to using client without cache
  [#1436](https://github.com/fluxcd/kustomize-controller/pull/1436)
- Fix secret value showing up in logs
  [#1372](https://github.com/fluxcd/kustomize-controller/pull/1372)

Improvements:
- [RFC-0010] Introduce KMS provider decryption with service account
  [#1426](https://github.com/fluxcd/kustomize-controller/pull/1426)
  [#1449](https://github.com/fluxcd/kustomize-controller/pull/1449)
  [#1456](https://github.com/fluxcd/kustomize-controller/pull/1456)
- Add `WaitForTermination` option to DeletionPolicy
  [#1444](https://github.com/fluxcd/kustomize-controller/pull/1444)
- Skip emitting events for suspended Kustomizations
  [#1396](https://github.com/fluxcd/kustomize-controller/pull/1396)
- Various dependency updates
  [#1458](https://github.com/fluxcd/kustomize-controller/pull/1458)
  [#1448](https://github.com/fluxcd/kustomize-controller/pull/1448)
  [#1433](https://github.com/fluxcd/kustomize-controller/pull/1433)
  [#1435](https://github.com/fluxcd/kustomize-controller/pull/1435)
  [#1429](https://github.com/fluxcd/kustomize-controller/pull/1429)
  [#1414](https://github.com/fluxcd/kustomize-controller/pull/1414)
  [#1410](https://github.com/fluxcd/kustomize-controller/pull/1410)
  [#1401](https://github.com/fluxcd/kustomize-controller/pull/1401)

## 1.5.1

**Release date:** 2025-02-25

This patch release fixes a bug introduced in v1.5.0
that was causing spurious logging for deprecated API versions
and sometimes failures on health checks.

In addition, all error logs resulting from SOPS decryption
failures have been sanitised.

Fixes:
- Fix secret value showing up in logs
  [#1372](https://github.com/fluxcd/kustomize-controller/pull/1372)
- Use lazy restmapper vendored from controller-runtime v0.19
  [#1377](https://github.com/fluxcd/kustomize-controller/pull/1377)

## 1.5.0

**Release date:** 2025-02-18

This minor release comes with various bug fixes and improvements.

The controller has been updated to Kustomize **v5.6**, please see the
`kubernetes-sigs/kustomize` [changelog](https://github.com/kubernetes-sigs/kustomize/releases)
for more details.

The Kustomization API now supports custom health checks for Custom
Resources through Common Expression Language (CEL) expressions.
See [docs](https://fluxcd.io/flux/components/kustomize/kustomizations/#health-check-expressions).

The controller now sends an origin revision from OCI artifact
annotations to notification-controller on events, which is
useful for updating commit statuses on the notification
providers that support this feature.
See [docs](https://fluxcd.io/flux/cheatsheets/oci-artifacts/#git-commit-status-updates).

It is now also possible to control whether or not kustomize-controller
will orphan resources when a Kustomization is deleted.
See [docs](https://fluxcd.io/flux/components/kustomize/kustomizations/#deletion-policy).

In addition, the Kubernetes dependencies have been updated to v1.32.1 and
various other controller dependencies have been updated to their latest
version.

Fixes:
- Clarify precedence in Kustomization substituteFrom
  [#1301](https://github.com/fluxcd/kustomize-controller/pull/1301)
- Remove deprecated object metrics from controllers
  [#1305](https://github.com/fluxcd/kustomize-controller/pull/1305)

Improvements:
- Enable decryption of secrets generated by Kustomize components
  [#1283](https://github.com/fluxcd/kustomize-controller/pull/1283)
- Added decryption of Kustomize patches and refactor SOPS tests
  [#1286](https://github.com/fluxcd/kustomize-controller/pull/1286)
- Allow control of finalization garbage collection
  [#1314](https://github.com/fluxcd/kustomize-controller/pull/1314)
- Add OCI revision to events
  [#1338](https://github.com/fluxcd/kustomize-controller/pull/1338)
- [RFC-0009] Add CEL custom healthchecks
  [#1344](https://github.com/fluxcd/kustomize-controller/pull/1344)
- Add GroupChangeLog feature gate to fix es indexing cardinality
  [#1361](https://github.com/fluxcd/kustomize-controller/pull/1361)
- Various dependency updates
  [#1302](https://github.com/fluxcd/kustomize-controller/pull/1302)
  [#1304](https://github.com/fluxcd/kustomize-controller/pull/1304)
  [#1310](https://github.com/fluxcd/kustomize-controller/pull/1310)
  [#1313](https://github.com/fluxcd/kustomize-controller/pull/1313)
  [#1318](https://github.com/fluxcd/kustomize-controller/pull/1318)
  [#1320](https://github.com/fluxcd/kustomize-controller/pull/1320)
  [#1330](https://github.com/fluxcd/kustomize-controller/pull/1330)
  [#1348](https://github.com/fluxcd/kustomize-controller/pull/1348)
  [#1352](https://github.com/fluxcd/kustomize-controller/pull/1352)
  [#1354](https://github.com/fluxcd/kustomize-controller/pull/1354)
  [#1359](https://github.com/fluxcd/kustomize-controller/pull/1359)
  [#1362](https://github.com/fluxcd/kustomize-controller/pull/1362)
  [#1364](https://github.com/fluxcd/kustomize-controller/pull/1364)
  [#1358](https://github.com/fluxcd/kustomize-controller/pull/1358)

## 1.4.0

**Release date:** 2024-09-27

This minor release comes with various bug fixes and improvements.

kustomize-controller in [sharded
deployment](https://fluxcd.io/flux/installation/configuration/sharding/)
configuration now supports cross-shard dependency check. This allows a
Kustomization to depend on other Kustomizations managed by different controller
shards.

In addition, the Kubernetes dependencies have been updated to v1.31.1 and
various other controller dependencies have been updated to their latest version.
The controller is now built with Go 1.23.

Fixes:
- Fix incorrect use of format strings with the conditions package.
  [#1198](https://github.com/fluxcd/kustomize-controller/pull/1198)

Improvements:
- Update Bucket API to v1
  [#1253](https://github.com/fluxcd/kustomize-controller/pull/1253)
- Allow cross-shard dependency check
  [#1248](https://github.com/fluxcd/kustomize-controller/pull/1248)
- docs: Clarify .spec.decryption.secretRef usage
  [#1242](https://github.com/fluxcd/kustomize-controller/pull/1242)
- Build with Go 1.23
  [#1230](https://github.com/fluxcd/kustomize-controller/pull/1230)
- Various dependency updates
  [#1165](https://github.com/fluxcd/kustomize-controller/pull/1165)
  [#1181](https://github.com/fluxcd/kustomize-controller/pull/1181)
  [#1212](https://github.com/fluxcd/kustomize-controller/pull/1212)
  [#1228](https://github.com/fluxcd/kustomize-controller/pull/1228)
  [#1229](https://github.com/fluxcd/kustomize-controller/pull/1229)
  [#1233](https://github.com/fluxcd/kustomize-controller/pull/1233)
  [#1239](https://github.com/fluxcd/kustomize-controller/pull/1239)
  [#1240](https://github.com/fluxcd/kustomize-controller/pull/1240)
  [#1243](https://github.com/fluxcd/kustomize-controller/pull/1243)
  [#1249](https://github.com/fluxcd/kustomize-controller/pull/1249)
  [#1250](https://github.com/fluxcd/kustomize-controller/pull/1250)
  [#1251](https://github.com/fluxcd/kustomize-controller/pull/1251)

## 1.3.0

**Release date:** 2024-05-06

This minor release comes with new features, improvements and bug fixes.

The controller has been updated to Kustomize **v5.4**, please see the
`kubernetes-sigs/kustomize` [changelog](https://github.com/kubernetes-sigs/kustomize/releases)
for more details.

The Flux `Kustomization` API gains two optional fields `.spec.namePrefix` and `.spec.nameSuffix`
that can be used to specify a prefix and suffix to be added to the names
of all managed resources.

The controller now supports the `--feature-gates=StrictPostBuildSubstitutions=true`
flag, when enabled the post-build substitutions will fail if a
variable without a default value is declared in files but is
missing from the input vars.

When using variable substitution with values that are numbers or booleans,
it is now possible to covert the values to strings, for more details see the 
[post-build documentation](https://github.com/fluxcd/kustomize-controller/blob/release/v1.3.x/docs/spec/v1/kustomizations.md#post-build-substitution-of-numbers-and-booleans).

In addition, the controller dependencies have been updated to Kubernetes v1.30
and controller-runtime v0.18. Various other dependencies have also been updated to
their latest version to patch upstream CVEs.

Lastly, the controller is now built with Go 1.22.

Improvements:
- Implement name prefix/suffix transformers
  [#1134](https://github.com/fluxcd/kustomize-controller/pull/1134)
- Add `StrictPostBuildSubstitutions` feature flag
  [#1130](https://github.com/fluxcd/kustomize-controller/pull/1130)
- Document how to use numbers and booleans in post build substitutions
  [#1129](https://github.com/fluxcd/kustomize-controller/pull/1129)
- Remove deprecated aad pod identity from API docs
  [#1152](https://github.com/fluxcd/kustomize-controller/pull/1152)
- api: Refer condition type constants from `fluxcd/pkg/apis`
  [#1144](https://github.com/fluxcd/kustomize-controller/pull/1144)
- Update dependencies to Kustomize v5.4.0
  [#1128](https://github.com/fluxcd/kustomize-controller/pull/1128)
- Various dependency updates
  [#1155](https://github.com/fluxcd/kustomize-controller/pull/1155)
  [#1121](https://github.com/fluxcd/kustomize-controller/pull/1121)
  [#1139](https://github.com/fluxcd/kustomize-controller/pull/1139)
  [#1122](https://github.com/fluxcd/kustomize-controller/pull/1122)

Fixes:
- Fix requeue warning introduced by controller-runtime
  [#1090](https://github.com/fluxcd/kustomize-controller/pull/1090)
- Remove effectless statement
  [#1091](https://github.com/fluxcd/kustomize-controller/pull/1091)
- Remove `genclient:Namespaced` tag
  [#1092](https://github.com/fluxcd/kustomize-controller/pull/1092)

## 1.2.2

**Release date:** 2024-02-01

This patch release comes with various bug fixes and improvements.

Reconciling empty directories and directories without Kubernetes manifests no
longer results in an error. This regressing bug was introduced with the
controller upgrade to Kustomize v5.3 and has been fixed in this patch release.

The regression due to which the namespaced objects without a namespace specified
resulted in `not found` error instead of `namespace not specified` has also been
fixed. And the regression due to which Roles and ClusterRoles were reconciled
over and over due to the normalization of Roles and ClusterRoles has also been
fixed.

In addition, the Kubernetes dependencies have been updated to v1.28.6. Various
other dependencies have also been updated to their latest version to patch
upstream CVEs.

Lastly, the controller is now built with Go 1.21.

Improvements:
- Update Go to 1.21
  [#1053](https://github.com/fluxcd/kustomize-controller/pull/1053)
- Various dependency updates
  [#1076](https://github.com/fluxcd/kustomize-controller/pull/1076)
  [#1074](https://github.com/fluxcd/kustomize-controller/pull/1074)
  [#1070](https://github.com/fluxcd/kustomize-controller/pull/1070)
  [#1068](https://github.com/fluxcd/kustomize-controller/pull/1068)
  [#1065](https://github.com/fluxcd/kustomize-controller/pull/1065)
  [#1060](https://github.com/fluxcd/kustomize-controller/pull/1060)
  [#1059](https://github.com/fluxcd/kustomize-controller/pull/1059)
  [#1051](https://github.com/fluxcd/kustomize-controller/pull/1051)
  [#1049](https://github.com/fluxcd/kustomize-controller/pull/1049)
  [#1046](https://github.com/fluxcd/kustomize-controller/pull/1046)
  [#1044](https://github.com/fluxcd/kustomize-controller/pull/1044)
  [#1040](https://github.com/fluxcd/kustomize-controller/pull/1040)
  [#1038](https://github.com/fluxcd/kustomize-controller/pull/1038)

## 1.2.1

**Release date:** 2023-12-14

This patch release comes with improvements in logging to provide faster feedback
on any HTTP errors encountered while fetching source artifacts.

In addition, the status condition messages are now trimmed to respect the size
limit defined by the API.

Improvements:
- Update runtime to v0.43.3
  [#1031](https://github.com/fluxcd/kustomize-controller/pull/1031)
- Log HTTP errors to provide faster feedback
  [#1028](https://github.com/fluxcd/kustomize-controller/pull/1028)

## 1.2.0

**Release date:** 2023-12-11

This minor release comes with performance improvements, bug fixes and several new features.

The controller has been updated from Kustomize v5.0 to **v5.3**, please the see
`kubernetes-sigs/kustomize` [changelog](https://github.com/kubernetes-sigs/kustomize/releases)
for a more details.

Starting with this version, the controller will automatically perform a cleanup of
the Pods belonging to stale Kubernetes Jobs after a force apply.

A new controller flag `--override-manager` has been added to extend the Field Managers disallow list.
Using this flag, cluster administrators can configure the controller to undo changes
made with Lens and other UI tools that directly modify Kubernetes objects on clusters.

In addition, the controller dependencies have been updated, including an update to Kubernetes v1.28.
The container base image has been updated to Alpine 3.19.

Improvements:
- Update source-controller to v1.2.2
  [#1024](https://github.com/fluxcd/kustomize-controller/pull/1024)
- build: update Alpine to 3.19
  [#1023](https://github.com/fluxcd/kustomize-controller/pull/1023)
- Update Kustomize to v5.3.0
  [#1021](https://github.com/fluxcd/kustomize-controller/pull/1021)
- Support additional Field Managers in the disallow list
  [#1017](https://github.com/fluxcd/kustomize-controller/pull/1017)
- Add test for Namespace custom resource
  [#1016](https://github.com/fluxcd/kustomize-controller/pull/1016)
- Update controller to Kubernetes v1.28.4
  [#1014](https://github.com/fluxcd/kustomize-controller/pull/1014)
- Disable status poller cache by default
  [#1012](https://github.com/fluxcd/kustomize-controller/pull/1012)
- Tweak permissions on various created files
  [#1005](https://github.com/fluxcd/kustomize-controller/pull/1005)
- Cleanup pods when recreating Kubernetes Jobs
  [#997](https://github.com/fluxcd/kustomize-controller/pull/997)
- Update SOPS to v3.8.1
  [#995](https://github.com/fluxcd/kustomize-controller/pull/995)

## 1.1.1

**Release date:** 2023-10-11

This patch release contains an improvement to retry the reconciliation of a
`Kustomization` as soon as the source artifact is available in storage.
Which is particularly useful when the source-controller has just been upgraded.

In addition, the controller can now detect immutable field errors returned by the
Google Cloud k8s-config-connector admission controller and recreate the GCP custom
resources annotated with `kustomize.toolkit.fluxcd.io/force: Enabled`.

Improvements:
- Update `fluxcd/pkg` dependencies
  [#983](https://github.com/fluxcd/kustomize-controller/pull/983)
- Bump `github.com/cyphar/filepath-securejoi`n from 0.2.3 to 0.2.4
  [#962](https://github.com/fluxcd/kustomize-controller/pull/962)

Fixes:
- fix: Retry when artifacts are available in storage
  [#980](https://github.com/fluxcd/kustomize-controller/pull/980)
- fix: Consistent artifact fetching retry timing
  [#978](https://github.com/fluxcd/kustomize-controller/pull/978)

## 1.1.0

**Release date:** 2023-08-23

This minor release comes with performance improvements, bug fixes and several new features.

The apply behaviour has been extended with two policies `IfNotPresent` and `Ignore`.
To change the apply behaviour for specific Kubernetes resources, you can annotate them with:

| Annotation                          | Default    | Values                                                         | Role            |
|-------------------------------------|------------|----------------------------------------------------------------|-----------------|
| `kustomize.toolkit.fluxcd.io/ssa`   | `Override` | - `Override`<br/>- `Merge`<br/>- `IfNotPresent`<br/>- `Ignore` | Apply policy    |
| `kustomize.toolkit.fluxcd.io/force` | `Disabled` | - `Enabled`<br/>- `Disabled`                                   | Recreate policy |
| `kustomize.toolkit.fluxcd.io/prune` | `Enabled`  | - `Enabled`<br/>- `Disabled`                                   | Delete policy   |

The `IfNotPresent` policy instructs the controller to only apply the Kubernetes resources if they are not present on the cluster.
This policy can be used for Kubernetes `Secrets` and `ValidatingWebhookConfigurations` managed by cert-manager,
where Flux creates the resources with fields that are later on mutated by other controllers.

This version improves the health checking with fail-fast behaviour
by detecting stalled Kubernetes rollouts.

In addition, the controller now stops exporting an object's
metrics as soon as the object has been deleted.

Lastly, this release introduces two controller flags:

- The `--concurrent-ssa` flag sets the number of concurrent server-side apply operations
  performed by the controller. Defaults to 4 concurrent operations per reconciliation.
- The `--interval-jitter-percentage` flag makes the
  controller distribute the load more evenly when multiple objects are set up
  with the same interval. The default of this flag is set to `5`, which means
  that the interval will be jittered by a +/- 5% random value (e.g. if the
  interval is 10 minutes, the actual reconciliation interval will be between 9.5
  and 10.5 minutes).

Improvements:
- Add `--concurrent-ssa` flag
  [#948](https://github.com/fluxcd/kustomize-controller/pull/948)
- Add `IfNotPresent` and `Ignore` SSA policies
  [#943](https://github.com/fluxcd/kustomize-controller/pull/943)
- controller: jitter requeue interval
  [#940](https://github.com/fluxcd/kustomize-controller/pull/940)
- Enable fail-fast behavior for health checks
  [#933](https://github.com/fluxcd/kustomize-controller/pull/933)
- Bump `fluxcd/pkg/ssa` to improve immutable error detection
  [#932](https://github.com/fluxcd/kustomize-controller/pull/932)
- Update dependencies
  [#939](https://github.com/fluxcd/kustomize-controller/pull/939)
- Update Source API to v1.1.0
  [#952](https://github.com/fluxcd/kustomize-controller/pull/952)

Fixes:
- Handle delete before adding finalizer
  [#930](https://github.com/fluxcd/kustomize-controller/pull/930)
- Delete stale metrics on object delete
  [#944](https://github.com/fluxcd/kustomize-controller/pull/944)

## 1.0.1

**Release date:** 2023-07-10

This is a patch release that fixes spurious events emitted for skipped resources.

Fixes:
- Exclude skipped resources from apply events
  [#920](https://github.com/fluxcd/kustomize-controller/pull/920)

## 1.0.0

**Release date:** 2023-07-04

This is the first stable release of the controller. From now on, this controller
follows the [Flux 2 release cadence and support pledge](https://fluxcd.io/flux/releases/).

Starting with this version, the build, release and provenance portions of the
Flux project supply chain [provisionally meet SLSA Build Level 3](https://fluxcd.io/flux/security/slsa-assessment/).

This release includes several bug fixes. In addition, dependencies have been updated
to their latest version, including an update of Kubernetes to v1.27.3.

For a comprehensive list of changes since `v0.35.x`, please refer to the
changelog for [v1.0.0-rc.1](#100-rc1), [v1.0.0-rc.2](#100-rc2),
[v1.0.0-rc.3](#100-rc3) and [`v1.0.0-rc.4](#100-rc4).

Improvements:
- Update dependencies
  [#908](https://github.com/fluxcd/kustomize-controller/pull/908)
- Align `go.mod` version with Kubernetes (Go 1.20)
  [#900](https://github.com/fluxcd/kustomize-controller/pull/900)

Fixes:
- Use kustomization namespace for empty dependency source namespace
  [#897](https://github.com/fluxcd/kustomize-controller/pull/897)
- docs: Clarify that targetNamespace namespace can be part of resources
  [#896](https://github.com/fluxcd/kustomize-controller/pull/896)

## 1.0.0-rc.4

**Release date:** 2023-05-29

This release candidate comes with support for Kustomize v5.0.3.

⚠️ Note that Kustomize v5 contains breaking changes, please consult their
[changelog](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv5.0.0)
for more details.

In addition, the controller dependencies have been updated to
Kubernetes v1.27.2 and controller-runtime v0.15.0.

Improvements:
- Update Kubernetes to v1.27 and Kustomize to v5
  [#850](https://github.com/fluxcd/kustomize-controller/pull/850)
- Update controller-runtime to v0.15.0
  [#869](https://github.com/fluxcd/kustomize-controller/pull/869)
- Update CA certificates
  [#872](https://github.com/fluxcd/kustomize-controller/pull/872)
- Update source-controller to v1.0.0-rc.4
  [#873](https://github.com/fluxcd/kustomize-controller/pull/873)

## 1.0.0-rc.3

**Release date:** 2023-05-12

This release candidate comes with improved error reporting for when
the controller fails to fetch an artifact due to a checksum mismatch.

In addition, the controller dependencies have been updated to patch
CVE-2023-1732 and the base image has been updated to Alpine 3.18.

Improvements:
- Update Alpine to 3.18
  [#855](https://github.com/fluxcd/kustomize-controller/pull/855)
- Update dependencies
  [#862](https://github.com/fluxcd/kustomize-controller/pull/862)
- build(deps): bump github.com/cloudflare/circl from 1.1.0 to 1.3.3
  [#860](https://github.com/fluxcd/kustomize-controller/pull/860)
- docs: Clarify the Kustomize components relative paths requirement
  [#861](https://github.com/fluxcd/kustomize-controller/pull/861)

## 1.0.0-rc.2

**Release date:** 2023-05-09

This release candidate fixes secrets decryption when using Azure Key Vault.

In addition, the controller dependencies have been updated to their latest
versions.

Improvements:
- Fix SOPS azkv envCred
  [#838](https://github.com/fluxcd/kustomize-controller/pull/838)
- Update dependencies
  [#853](https://github.com/fluxcd/kustomize-controller/pull/853)

## 1.0.0-rc.1

**Release date:** 2023-04-03

This release candidate promotes the `Kustomization` API from `v1beta2` to `v1`.
The controller now supports horizontal scaling using
sharding based on a label selector.

In addition, the controller now supports Workload Identity when
decrypting secrets with SOPS and Azure Vault.

### Highlights

This release candidate requires the `GitRepository` API version `v1`,
first shipped with [source-controller](https://github.com/fluxcd/source-controller)
v1.0.0-rc.1.

#### API changes

The `Kustomization` kind was promoted from v1beta2 to v1 (GA) and deprecated fields were removed.

A new optional field called `CommonMetadata` was added to the API
for setting labels and/or annotations to all resources part of a Kustomization.
The main difference to the Kustomize
[commonLabels](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/commonlabels/) and
[commonAnnotations](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/commonannotations/),
is that the controller sets the labels and annotations only to the top level `metadata` field,
without patching the Kubernetes Deployment `spec.template` or the Service `spec.selector`.

The `kustomizations.kustomize.toolkit.fluxcd.io` CRD contains the following versions:
- v1 (storage version)
- v1beta2 (deprecated)
- v1beta1 (deprecated)

#### Upgrade procedure

The `Kustomization` v1 API is backwards compatible with v1beta2, except for the following:
- the deprecated field `.spec.validation` was removed
- the deprecated field `.spec.patchesStrategicMerge` was removed (replaced by `.spec.patches`)
- the deprecated field `.spec.patchesJson6902 ` was removed (replaced by `.spec.patches`)

To upgrade from v1beta2, after deploying the new CRD and controller,
set  `apiVersion: kustomize.toolkit.fluxcd.io/v1` in the YAML files that contain
`Kustomization` definitions and remove the deprecated fields if any.
Bumping the API version in manifests can be done gradually.
It is advised to not delay this procedure as the beta versions will be removed after 6 months.

#### Sharding

Starting with this release, the controller can be configured with
`--watch-label-selector`, after which only objects with this label will
be reconciled by the controller.

This allows for horizontal scaling, where kustomize-controller
can be deployed multiple times with a unique label selector
which is used as the sharding key.

### Full changelog

Improvements:
- GA: Promote Kustomization API to `kustomize.toolkit.fluxcd.io/v1`
  [#822](https://github.com/fluxcd/kustomize-controller/pull/822)
- Add common labels and annotations patching capabilities
  [#817](https://github.com/fluxcd/kustomize-controller/pull/817)
- Add reconciler sharding capability based on label selector
  [#821](https://github.com/fluxcd/kustomize-controller/pull/821)
- Support Workload Identity for Azure Vault
  [#813](https://github.com/fluxcd/kustomize-controller/pull/813)
- Verify Digest of Artifact
  [#818](https://github.com/fluxcd/kustomize-controller/pull/818)
- Move `controllers` to `internal/controllers`
  [#820](https://github.com/fluxcd/kustomize-controller/pull/820)
- build(deps): bump github.com/opencontainers/runc from 1.1.2 to 1.1.5
  [#824](https://github.com/fluxcd/kustomize-controller/pull/824)

## 0.35.1

**Release date:** 2023-03-20

This prerelease comes with a fix to error reporting.
The controller will now reveal validation errors when force applying
resources with immutable field changes.

In addition, the controller dependencies have been updated to their latest
versions.

Improvements:
- Update dependencies
  [#814](https://github.com/fluxcd/kustomize-controller/pull/814)

## 0.35.0

**Release date:** 2023-03-08

This prerelease adds support for disabling the cache of the `kstatus` status
poller, which is used to determine the health of the resources applied by the
controller. To disable the cache, configure the Deployment of the controller
with `--feature-gates=DisableStatusPollerCache=true`.

This may have a positive impact on memory usage on large clusters with many
objects, at the cost of an increased number of API calls.

In addition, `klog` has been configured to log using the same logger as the
rest of the controller (providing a consistent log format).

Lastly, the controller is now built using Go `1.20`, and the dependencies have
been updated to their latest versions.

Improvements:
- api: update description LastAppliedRevision
  [#798](https://github.com/fluxcd/kustomize-controller/pull/798)
- Update Go to 1.20
  [#806](https://github.com/fluxcd/kustomize-controller/pull/806)
- Update dependencies
  [#807](https://github.com/fluxcd/kustomize-controller/pull/807)
  [#811](https://github.com/fluxcd/kustomize-controller/pull/811)
- Use `logger.SetLogger` to also configure `klog`
  [#809](https://github.com/fluxcd/kustomize-controller/pull/809)

## 0.34.0

**Release date:** 2023-02-17

This prerelease adds support for parsing the
[RFC-0005](https://github.com/fluxcd/flux2/tree/main/rfcs/0005-artifact-revision-and-digest)
revision format produced by source-controller `>=v0.35.0`.

In addition, the controller dependencies have been updated to their latest
versions.

Improvements:
- Support RFC-0005 revision format
  [#793](https://github.com/fluxcd/kustomize-controller/pull/793)
- Update dependencies
  [#796](https://github.com/fluxcd/kustomize-controller/pull/796)

## 0.33.0

**Release date:** 2023-02-01

This prerelease comes with support for recreating immutable resources (e.g. Kubernetes Jobs)
by annotating or labeling them with `kustomize.toolkit.fluxcd.io/force: enabled`.

The caching of Secret and ConfigMap resources has been disabled to improve memory usage.
To opt-out from this behavior, start the controller with: `--feature-gates=CacheSecretsAndConfigMaps=true`.

In addition, the controller dependencies have been updated to
Kubernetes v1.26.1 and controller-runtime v0.14.2. The controller base image has 
been updated to Alpine 3.17 (which contains CVE fixes for OS packages).

Improvements:
- Allow force apply to be configured in metadata
  [#787](https://github.com/fluxcd/kustomize-controller/pull/787)
- Disable caching of Secrets and ConfigMaps
  [#789](https://github.com/fluxcd/kustomize-controller/pull/789)
- build: Enable SBOM and SLSA Provenance
  [#787](https://github.com/fluxcd/kustomize-controller/pull/788)
- build: Update Alpine to 3.17
  [#786](https://github.com/fluxcd/kustomize-controller/pull/786)
- build: pdate source-controller/api to v0.34.0
  [#790](https://github.com/fluxcd/kustomize-controller/pull/790)
- build: Download CRD deps only when necessary
  [#783](https://github.com/fluxcd/kustomize-controller/pull/783)
- test: Enable kstatus checks
  [#784](https://github.com/fluxcd/kustomize-controller/pull/784)

## 0.32.0

**Release date:** 2022-12-20

This prerelease comes with experimental support for
[Kustomize components](https://github.com/fluxcd/kustomize-controller/blob/v0.32.0/docs/spec/v1beta2/kustomization.md#components).

In addition, the AWS and Google Cloud KMS dependencies have been updated
to match the latest stable release from upstream.

Improvements:
- Add support for Kustomize components
  [#754](https://github.com/fluxcd/kustomize-controller/pull/754)
- Update dependencies
  [#780](https://github.com/fluxcd/kustomize-controller/pull/780)
- Document the behaviour of atomic fields with server-side apply
  [#774](https://github.com/fluxcd/kustomize-controller/pull/774)
- fuzz: Use build script from upstream and fix fuzzers
  [#777](https://github.com/fluxcd/kustomize-controller/pull/777)
- build: Fix cifuzz tests and improve fuzz tests' reliability
  [#771](https://github.com/fluxcd/kustomize-controller/pull/771)
- build: update dockertest to Go Mod compatible v3
  [#776](https://github.com/fluxcd/kustomize-controller/pull/776)

## 0.31.0

**Release date:** 2022-11-18

This prerelease comes with improvements to the manifests
generation component. The Kustomize overlay build logic has been
factored out into `github.com/fluxcd/pkg/kustomize` so that both
the controller and the Flux CLI (`flux buid kustomization`)
share the same code base.

In addition, the controller dependencies have been updated to
Kubernetes v1.25.4 and controller-runtime v0.13.1.
The Azure Vault SDK used for secrets decryption has been updated
to match the latest stable release from upstream.

Improvements:
- Refactor: Generate manifests with `flux/pkg/kustomize`
  [#763](https://github.com/fluxcd/kustomize-controller/pull/763)
- Update `keyvault/azkeys` Azure SDK to v0.9.0
  [#759](https://github.com/fluxcd/kustomize-controller/pull/759)
- Update Source API to v0.32.1
  [#768](https://github.com/fluxcd/kustomize-controller/pull/768)
- Update dependencies
  [#767](https://github.com/fluxcd/kustomize-controller/pull/767)
- Use Flux Event API v1beta1
  [#758](https://github.com/fluxcd/kustomize-controller/pull/758)
- build: Bump gpg to alpine's edge
  [#760](https://github.com/fluxcd/kustomize-controller/pull/760)
- build: Remove nsswitch.conf creation
  [#765](https://github.com/fluxcd/kustomize-controller/pull/765)

Fixes:
- Don't override the reconcile error on status patching
  [#761](https://github.com/fluxcd/kustomize-controller/pull/761)

## 0.30.0

**Release date:** 2022-10-21

This prerelease comes with new status condition named `Reconciling` which improves
the observability for the actions performed by the controller during a reconciliation run.
The `Kustomization.status.conditions` have been aligned with Kubernetes
standard conditions and kstatus.

In addition, the controller memory usage was reduced by 90% when performing artifact
operations and can now better handle the reconciliation of large sources in-parallel.

Improvements:
- Optimise the memory usage of artifact operations
  [#747](https://github.com/fluxcd/kustomize-controller/pull/747)
- Refactor: Adopt Flux runtime conditions and status standards
  [#745](https://github.com/fluxcd/kustomize-controller/pull/745)
- Refactor: Remove docs which overlap with Flux website
  [#746](https://github.com/fluxcd/kustomize-controller/pull/746)
- Refactor: Move inventory helpers to internal package
  [#744](https://github.com/fluxcd/kustomize-controller/pull/744)
- Refactor: Acquire artifacts with `fluxcd/pkg/http/fetch`
  [#743](https://github.com/fluxcd/kustomize-controller/pull/743)
- Refactor: Use impersonation from `fluxcd/pkg/runtime/client`
  [#742](https://github.com/fluxcd/kustomize-controller/pull/742)
- Refactor: Extract generator to internal package
  [#740](https://github.com/fluxcd/kustomize-controller/pull/740)
- Refactor: Extract decrytor to internal package
  [#739](https://github.com/fluxcd/kustomize-controller/pull/739)
- Support alternative kustomization file names
  [#738](https://github.com/fluxcd/kustomize-controller/pull/738)
- API: allow configuration of `h` unit for timeouts
  [#749](https://github.com/fluxcd/kustomize-controller/pull/749)
- Update dependencies
  [#750](https://github.com/fluxcd/kustomize-controller/pull/750)

## 0.29.0

**Release date:** 2022-09-29

This prerelease comes with strict validation rules for API fields which define a
(time) duration. Effectively, this means values without a time unit (e.g. `ms`,
`s`, `m`, `h`) will now be rejected by the API server. To stimulate sane
configurations, the units `ns`, `us` and `µs` can no longer be configured, nor
can `h` be set for fields defining a timeout value.

In addition, the controller dependencies have been updated
to Kubernetes controller-runtime v0.13.

:warning: **Breaking changes:**
- `.spec.interval` new validation pattern is `"^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"`
- `.spec.retryInterval` new validation pattern is `"^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"`
- `.spec.timeout` new validation pattern is `"^([0-9]+(\\.[0-9]+)?(ms|s|m))+$"`

Improvements:
- api: add custom validation for v1.Duration types
  [#731](https://github.com/fluxcd/kustomize-controller/pull/731)
- Build with Go 1.19
  [#733](https://github.com/fluxcd/kustomize-controller/pull/733)
- Update dependencies
  [#735](https://github.com/fluxcd/kustomize-controller/pull/735)

Fixes:
- Fix health checking for global objects
  [#730](https://github.com/fluxcd/kustomize-controller/pull/730)

## 0.28.0

**Release date:** 2022-09-12

This prerelease comes with improvements to reconciling Kubernetes class type objects,
SOPS decryption and fuzzing. In addition, the controller dependencies have been updated
to Kubernetes controller-runtime v0.12.

:warning: **Breaking change:** The controller logs have been aligned
with the Kubernetes structured logging. For more details on the new logging
structure please see: [fluxcd/flux2#3051](https://github.com/fluxcd/flux2/issues/3051).

Improvements:
- Align controller logs to Kubernetes structured logging
  [#718](https://github.com/fluxcd/kustomize-controller/pull/718)
- Reconcile Kubernetes class type objects in a dedicated stage
  [#720](https://github.com/fluxcd/kustomize-controller/pull/720)
- Sort SOPS masterkeys so offline decrypt methods are tried first
  [#726](https://github.com/fluxcd/kustomize-controller/pull/726)
- SOPS: Update the AWS SDK for KMS
  [#721](https://github.com/fluxcd/kustomize-controller/pull/721)
- Refactor Fuzzers based on Go native fuzzing
  [#723](https://github.com/fluxcd/kustomize-controller/pull/723)
- Fuzz optimisations
  [#722](https://github.com/fluxcd/kustomize-controller/pull/722)
- Update dependencies
  [#724](https://github.com/fluxcd/kustomize-controller/pull/724)

## 0.27.1

**Release date:** 2022-08-29

This prerelease comes with panic recovery,
to protect the controller from crashing when reconciliations lead to a crash.

In addition, the controller dependencies have been updated to Kubernetes v1.25.0.

Improvements:
- Enable RecoverPanic option on reconciler
  [#708](https://github.com/fluxcd/kustomize-controller/pull/708)
- Update Kubernetes packages to v1.25.0
  [#714](https://github.com/fluxcd/kustomize-controller/pull/714)
- Add file path to sops decryption errors
  [#706](https://github.com/fluxcd/kustomize-controller/pull/706)
- Update doc on target namespace
  [#712](https://github.com/fluxcd/kustomize-controller/pull/712)

## 0.27.0

**Release date:** 2022-08-08

This prerelease comes with support for the `OCIRepository` source type.

In addition, the controller has been updated to Kubernetes v1.24.3
and Kustomize v4.5.7.

Features:
- Add support for OCIRepository sources
  [#684](https://github.com/fluxcd/kustomize-controller/pull/684)

Improvements:
- Update dependencies
  [#704](https://github.com/fluxcd/kustomize-controller/pull/704)

## 0.26.3

**Release date:** 2022-07-13

This prerelease adds a retry mechanism for "not found" errors when
downloading artifacts and recovers from SOPS store panics.
Some dependencies have also been updated to patch upstream CVEs.

Fixes:
- decryptor: recover from SOPS store panic
  [#691](https://github.com/fluxcd/kustomize-controller/pull/691)

Improvements:
- Retry downloading artifacts on not found errors
  [#689](https://github.com/fluxcd/kustomize-controller/pull/689)
- Update dependencies
  [#692](https://github.com/fluxcd/kustomize-controller/pull/692)
  [#696](https://github.com/fluxcd/kustomize-controller/pull/696)
- build: Upgrade to Go 1.18
  [#694](https://github.com/fluxcd/kustomize-controller/pull/694)

## 0.26.2

**Release date:** 2022-06-29

This prerelease adds support for health checking Kubernetes Jobs 
when impersonating a service account.

Fixes:
- Fix job wait by adding polling options to impersonation client
  [#687](https://github.com/fluxcd/kustomize-controller/pull/687)

## 0.26.1

**Release date:** 2022-06-08

This prerelease comes with documentation improvements on how to generate
image pull secrets from SOPS encrypted `.dockerconfigjson` files.

In addition, the controller has been updated to Kubernetes v1.24.1.

Improvements:
- docs: Add example section and dockerconfigjson encryption
  [#675](https://github.com/fluxcd/kustomize-controller/pull/675)
- Update dependencies
  [#676](https://github.com/fluxcd/kustomize-controller/pull/676)
- Update fluxcd/pkg dependencies
  [#677](https://github.com/fluxcd/kustomize-controller/pull/677)

## 0.26.0

**Release date:** 2022-06-01

This prerelease comes with support for configuring the authentication to
AWS KMS, Azure Key Vault and GCP KMS on multi-tenant clusters.
A tenant can create a secret in their namespace with their KMS credentials
and supply it to Flux using `Kustomization.spec.decryption.secretRef`.
For more details on how to configure SOPS decryption with KMS, see the
[docs](https://github.com/fluxcd/kustomize-controller/blob/v0.26.0/docs/spec/v1beta2/kustomization.md#secrets-decryption).

Starting with this version, the controller conforms to the Kubernetes
[API Priority and Fairness](https://kubernetes.io/docs/concepts/cluster-administration/flow-control/).
The controller detects if the server-side throttling is enabled and uses the
advertised rate limits. When server-side throttling is enabled, the controller
ignores the `--kube-api-qps` and `--kube-api-burst` flags.

In addition, Kustomize has been updated
to [`v4.5.5`](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv4.5.5) 
which comes with significant performance improvements for OpenAPI parsing.

Improvements:
- Support AWS KMS credentials using decryption secretRef
  [#641](https://github.com/fluxcd/kustomize-controller/pull/641)
  [#667](https://github.com/fluxcd/kustomize-controller/pull/667)
- Support GCP KMS credentials using decryption secretRef
  [#635](https://github.com/fluxcd/kustomize-controller/pull/635)
- Update SOPS to v3.7.3
  [#647](https://github.com/fluxcd/kustomize-controller/pull/647)
- Update controller to kustomize v4.5.5
  [#660](https://github.com/fluxcd/kustomize-controller/pull/660)
- Update dependencies
  [#650](https://github.com/fluxcd/kustomize-controller/pull/650)
- Update Alpine to v3.16
  [#661](https://github.com/fluxcd/kustomize-controller/pull/661)
- Update go-yaml to v3.0.0
  [#665](https://github.com/fluxcd/kustomize-controller/pull/665)
- Update source-controller/api to v0.25.0
  [#671](https://github.com/fluxcd/kustomize-controller/pull/671)

Fixes:
- Set digests in image override
  [#655](https://github.com/fluxcd/kustomize-controller/pull/655)

## 0.25.0

**Release date:** 2022-05-03

This prerelease adds support for disallowing remote bases in Kustomize overlays
using `--no-remote-bases=true` (default: `false`).  When this flag is enabled on
the controller, all resources must refer to local files included in the Source
Artifact, meaning only the Flux Sources can affect the cluster-state. Users
are encouraged to enable it on production systems for security and performance
reasons.

In addition, support has been introduced for defining a KubeConfig Secret data
key in the `.spec.kubeConfig.SecretRef.key` (default: `value` or `value.yaml`),
and dependencies have been updated.

Improvements:
- Support defining a KubeConfig Secret data key
  [#615](https://github.com/fluxcd/kustomize-controller/pull/615)
  [#645](https://github.com/fluxcd/kustomize-controller/pull/645)
- Disallow remote bases usage in Kustomize overlays
  [#638](https://github.com/fluxcd/kustomize-controller/pull/638)
- decryptor: improve detection of in and out formats for Secret data fields
  [#644](https://github.com/fluxcd/kustomize-controller/pull/644)

## 0.24.4

**Release date:** 2022-04-28

This prerelease ensures we recover from Kustomize build panics to guarantee
continuity of operations when running into invalid object data.

In addition, handling of file formats while decrypting Secret generator entries
with SOPS has been improved to ensure encrypted files in format A can be
decrypted to target format B.

Fixes:
- Use Secret generator keys for SOPS format hint
  [#636](https://github.com/fluxcd/kustomize-controller/pull/636)

Improvements:
- generator: recover from kustomize build panics
  [#637](https://github.com/fluxcd/kustomize-controller/pull/637)

## 0.24.3

**Release date:** 2022-04-22

This prerelease allows for configuring the exponential back-off retry, already
introduced in other Flux controllers. It can be configured with the new flags:
`--min-retry-delay` (default: `750ms`) and `--max-retry-delay`
(default: `15min`). Previously the defaults were set to `5ms` and `1000s`.

Fixes:
- Ensure generated temp dir is absolute on all OSes
  [#630](https://github.com/fluxcd/kustomize-controller/pull/630)

Improvements:
- feat: flags to configure exponential backoff retry
  [#631](https://github.com/fluxcd/kustomize-controller/pull/631)
- Update source-controller to v0.24.1
  [#632](https://github.com/fluxcd/kustomize-controller/pull/632)

## 0.24.2

**Release date:** 2022-04-21

This prerelease updates the Go `golang.org/x/crypto` dependency to latest to
please static security analysers (CVE-2022-27191).

Fixes:
- Update golang.org/x/crypto
  [#628](https://github.com/fluxcd/kustomize-controller/pull/628)

## 0.24.1

**Release date:** 2022-04-20

This prerelease fixes a regression bug introduced in
[#620](https://github.com/fluxcd/kustomize-controller/pull/620), which
prevented remote build directories from being reachable within the FS.

Fixes:
- generator: ensure remote build dirs can be reached
  [#626](https://github.com/fluxcd/kustomize-controller/pull/626)

## 0.24.0

**Release date:** 2022-04-19

This prerelease matures the Kustomize decryptor service, which handles the
actual decryption of SOPS' encrypted Secrets, and now allows decrypting
[file sources](https://pkg.go.dev/sigs.k8s.io/kustomize@v1.0.2/pkg/types#DataSources)
referenced in Kustomization files.

In addition, Kustomize now operates using our own file system implementation,
and dependencies have been updated to their latest versions.

Improvements:
- controllers: improve decryptor and add tests
  [#619](https://github.com/fluxcd/kustomize-controller/pull/619)
- controllers: use own Kustomize FS implementation
  [#620](https://github.com/fluxcd/kustomize-controller/pull/620)
- Update dependencies
  [#621](https://github.com/fluxcd/kustomize-controller/pull/621)
  [#622](https://github.com/fluxcd/kustomize-controller/pull/622)

## 0.23.0

**Release date:** 2022-04-05

This prerelease adds some breaking changes around the use and handling of kubeconfigs 
files for remote reconciliations. Implements health checks for kubernetes jobs and 
updates documentation.

SOPS implementation was refactored to include various improvements and extended code
coverage. Age identities are now imported once and reused multiple times, optimising
CPU and memory usage between decryption operations.

**Breaking changes**:

- Use of file-based KubeConfig options are now permanently disabled (e.g. 
`TLSClientConfig.CAFile`, `TLSClientConfig.KeyFile`, `TLSClientConfig.CertFile`
and `BearerTokenFile`). The drive behind the change was to discourage
insecure practices of mounting Kubernetes tokens inside the controller's container file system.
- Use of `TLSClientConfig.Insecure` in KubeConfig file is disabled by default,
but can enabled at controller level with the flag `--insecure-kubeconfig-tls`.
- Use of `ExecProvider` in KubeConfig file is now disabled by default,
but can enabled at controller level with the flag `--insecure-kubeconfig-exec`.

Improvements:
- Add kubeconfig flags
  [#593](https://github.com/fluxcd/kustomize-controller/pull/593)
- sops: various improvements and tests
  [#607](https://github.com/fluxcd/kustomize-controller/pull/607)
- docs/spec/v1beta2: fix recommended settings typo
  [#609](https://github.com/fluxcd/kustomize-controller/pull/609)
- Implement health check for Kubernetes jobs
  [#608](https://github.com/fluxcd/kustomize-controller/pull/608)
- Update KubeConfig documentation
  [#611](https://github.com/fluxcd/kustomize-controller/pull/611)

## 0.22.3

**Release date:** 2022-03-29

This prerelease fixes a compatability issue between SOPS' Azure Key Vault
integration, and the controller's. In addition, Kustomize has been updated
to [`v4.5.4`](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv4.5.4)
to address an issue with ConfigMap and Secret generators.

Fixes:
- sops/azkv: ensure compatibility with upstream
  [#604](https://github.com/fluxcd/kustomize-controller/pull/604)
- Update Kustomize to v4.5.4
  [#606](https://github.com/fluxcd/kustomize-controller/pull/606)

## 0.22.2

**Release date:** 2022-03-25

This prerelease fixes a regression bug where the SOPS keyservice would not
properly fall back to the default server for Azure Key Vault decryption
requests.

In addition, Kustomize has been updated to `v4.5.3` to address an issue with
YAML anchors.

Improvements:
- Update Kustomize to v4.5.3
  [#594](https://github.com/fluxcd/kustomize-controller/pull/594)
- Update source-controller API to v0.22.3
  [#596](https://github.com/fluxcd/kustomize-controller/pull/596)

Fixes:
- sops/keyservice: properly fallback to default
  [#597](https://github.com/fluxcd/kustomize-controller/pull/597)

## 0.22.1

**Release date:** 2022-03-24

This prerelease fixes a regression bug where alerts are sent for every
reconciliation run.

Fixes:
- Ensure event annotations are prefixed with Group FQDN
  [#591](https://github.com/fluxcd/kustomize-controller/pull/591)

## 0.22.0

**Release date:** 2022-03-21

This prerelease introduces a new annotation `kustomize.toolkit.fluxcd.io/ssa: merge`
for allowing kustomize-controller to patch cluster addons such as CoreDNS without removing
the kubectl managed fields.

The source-controller dependency was updated to version `v0.22` which 
introduces API `v1beta2` and deprecates `v1beta1`.

In addition, various dependencies where updated to their latest versions, and
the code base was refactored to align with `fluxcd/pkg/runtime` v0.13 release.

Improvements:
- Allow shared ownership of in-cluster objects applied with kubectl
  [#581](https://github.com/fluxcd/kustomize-controller/pull/581)
- Update `pkg/runtime` and `apis/meta`
  [#575](https://github.com/fluxcd/kustomize-controller/pull/575)
- Update dependencies
  [#584](https://github.com/fluxcd/kustomize-controller/pull/584)
- SOPS: Add support for Azure Key Vault credentials
  [#495](https://github.com/fluxcd/kustomize-controller/pull/495)

## 0.21.1

**Release date:** 2022-02-23

This prerelease comes with a workaround for an
[upstream bug in Kubernetes](https://github.com/kubernetes/kubernetes/issues/108008),
where the keys set in a Secret with `stringData` are not removed from the cluster
when the keys are deleted from the manifest.

Improvements:
- Convert `stringData` to `data` before applying Secrets
  [#576](https://github.com/fluxcd/kustomize-controller/pull/576)
- Update dependencies
  [#574](https://github.com/fluxcd/kustomize-controller/pull/574)

## 0.21.0

**Release date:** 2022-02-16

This prerelease comes with support for making the Kubernetes Secrets and ConfigMaps
referenced in `postBuild.substituteFrom` optional.
When `substituteFrom.optional` is set to `true`, the controller will ignore
not found errors, and will substitute the variables with their default values.

Features:
- Tolerate absence of resources in post-build substitution
  [#570](https://github.com/fluxcd/kustomize-controller/pull/570)

## 0.20.2

**Release date:** 2022-02-10

This prerelease comes with an update to the `sigs.k8s.io/kustomize/api` package,
bringing the controller on par with the Kustomize v4.5.2 release.

Kustomize v4.5.2 contains a regression bug fix for
[pseudo git HTTP URLs](https://github.com/kubernetes-sigs/kustomize/pull/4453).

Improvements:
- Update controller to kustomize v4.5.2
  [#567](https://github.com/fluxcd/kustomize-controller/pull/567)
- Clarify `spec.path` in API docs
  [#566](https://github.com/fluxcd/kustomize-controller/pull/566)
- Fix typo in API docs
  [#564](https://github.com/fluxcd/kustomize-controller/pull/564)

## 0.20.1

**Release date:** 2022-02-07

This prerelease comes with an update to the `sigs.k8s.io/kustomize` packages, bringing 
the controller on par with the Kustomize v4.5.1 release.

Improvements:
- Update controller to kustomize v4.5.1
  [#559](https://github.com/fluxcd/kustomize-controller/pull/559)

Fixes:
* Transfer ownership of the kubectl managed fields
  [#562](https://github.com/fluxcd/kustomize-controller/pull/562)

## 0.20.0

**Release date:** 2022-02-01

This prerelease comes with security improvements for multi-tenant clusters:
- Platform admins can enforce impersonation across the cluster using the `--default-service-account` flag.
  When the flag is set, all `Kustomizations`, which don't have `spec.serviceAccountName` specified,
  use the service account name provided by `--default-service-account=<SA Name>` in the namespace of the object.
- Platform admins can disable cross-namespace references with the `--no-cross-namespace-refs=true` flag.
  When this flag is set, `Kustomizations` can only refer to sources (`GitRepositories` and `Buckets`)
  in the same namespace as the `Kustomization` object, preventing tenants from accessing another tenant's repositories.

The controller container images are signed with
[Cosign and GitHub OIDC](https://github.com/sigstore/cosign/blob/22007e56aee419ae361c9f021869a30e9ae7be03/KEYLESS.md),
and a Software Bill of Materials in [SPDX format](https://spdx.dev) has been published on the release page.

Starting with this version, the controller deployment conforms to the
Kubernetes [restricted pod security standard](https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted):
- all Linux capabilities were dropped
- the root filesystem was set to read-only
- the seccomp profile was set to the runtime default
- run as non-root was enabled
- the user and group ID was set to 65534

**Breaking changes**:
- The use of new seccomp API requires Kubernetes 1.19.
- The controller container is now executed under 65534:65534 (userid:groupid).
  This change may break deployments that hard-coded the user ID of 'controller' in their PodSecurityPolicy.
- When both `spec.kubeConfig` and `spec.ServiceAccountName` are specified, the controller will impersonate
  the service account on the target cluster, previously the controller ignored the service account.

Features:
- Allow setting a default service account for impersonation
  [#550](https://github.com/fluxcd/kustomize-controller/pull/550)
- Allow disabling cross-namespace references
  [#549](https://github.com/fluxcd/kustomize-controller/pull/549)
- SOPS: Add support for HashiCorp Vault token-based authentication
  [#538](https://github.com/fluxcd/kustomize-controller/pull/538)

Improvements:
- Publish SBOM and sign release artifacts
  [#541](https://github.com/fluxcd/kustomize-controller/pull/541)
- Drop capabilities, enable seccomp and enforce runAsNonRoot
  [#539](https://github.com/fluxcd/kustomize-controller/pull/539)
- docs: Add var substitution operator escape syntax
  [#537](https://github.com/fluxcd/kustomize-controller/pull/537)
- Update development documentation
  [#540](https://github.com/fluxcd/kustomize-controller/pull/540)
- Refactor Fuzz implementation
  [#536](https://github.com/fluxcd/kustomize-controller/pull/536)

Fixes:
* Revoke kubectl managed fields ownership
  [#527](https://github.com/fluxcd/kustomize-controller/pull/527)
* Ensure object are finalized under impersonation
  [#552](https://github.com/fluxcd/kustomize-controller/pull/552)
* Use patch instead of update when adding finalizers
  [#535](https://github.com/fluxcd/kustomize-controller/pull/535)
* Fix preflight validation
  [#544](https://github.com/fluxcd/kustomize-controller/pull/544)
* Fix the missing protocol for the first port in manager config
  [#547](https://github.com/fluxcd/kustomize-controller/pull/547)

## 0.19.1

**Release date:** 2022-01-13

This prerelease fixes a regression bug introduced in v0.19.0 that prevented `StatefulSets`
from being reconciled on Kubernetes <= 1.21.

Fixes:
* Update `fluxcd/pkg/ssa` to exclude the status field from apply
  [#533](https://github.com/fluxcd/kustomize-controller/pull/533)

## 0.19.0

**Release date:** 2022-01-10

This prerelease comes with an update to the Kubernetes and controller-runtime dependencies
to align them with the Kubernetes 1.23 release.

In addition, the controller is now built with Go 1.17 and Alpine 3.15.

Improvements:
* Update Go to v1.17 and controller-runtime to v0.11
  [#478](https://github.com/fluxcd/kustomize-controller/pull/478)
* Add condition to checkDependencies when SourceRef is the same
  [#521](https://github.com/fluxcd/kustomize-controller/pull/521)

Fixes:
* Fix HPA diff (upstream bug)
  [#526](https://github.com/fluxcd/kustomize-controller/pull/526)
* Fix inconsistent code-style raised at security audit
  [#522](https://github.com/fluxcd/kustomize-controller/pull/522)

## 0.18.2

**Release date:** 2021-12-09

This prerelease comes with improvements to force applying objects with immutable fields changes. 

Improvements:
* Update dependencies (fix CVE-2021-43784)
  [#509](https://github.com/fluxcd/kustomize-controller/pull/509)
* Update golang.org/x/text to v0.3.7 (fix CVE-2021-38561)
  [#512](https://github.com/fluxcd/kustomize-controller/pull/512)
* Add test for replacing variables in secrets
  [#505](https://github.com/fluxcd/kustomize-controller/pull/505)
* Document behaviour when changes are made to fields not stored in git
  [#501](https://github.com/fluxcd/kustomize-controller/pull/501)
* SOPS: ensure proper wiring to default server
  [#513](https://github.com/fluxcd/kustomize-controller/pull/513)

## 0.18.1

**Release date:** 2021-11-23

This prerelease replaces deprecated dependencies, most notably being the OpenPGP
package where `golang.org/x/crypto/openpgp` was replaced with
`github.com/ProtonMail/go-crypto/openpgp`.

Improvements:
* Update `source-controller/api` to v0.19.0
  [#499](https://github.com/fluxcd/kustomize-controller/pull/499)
* Replace deprecated dependencies
  [#498](https://github.com/fluxcd/kustomize-controller/pull/498)
* Update `opencontainers/{image-spec, runc}`
  [#497](https://github.com/fluxcd/kustomize-controller/pull/497)

## 0.18.0

**Release date:** 2021-11-12

This prerelease comes with artifact integrity verification.
During the acquisition of an artifact, kustomize-controller computes its checksum using SHA-2
and verifies that it matches the checksum advertised in the `Status` of the Source.

The controller dependencies has been updated to match
kustomize [v4.4.1](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv4.4.1).

Improvements:
* Verify artifacts integrity
  [#489](https://github.com/fluxcd/kustomize-controller/pull/489)
* Update controller to kustomize v4.4.1
  [#488](https://github.com/fluxcd/kustomize-controller/pull/488)

## 0.17.0

**Release date:** 2021-11-09

This prerelease comes with support for ignoring changes made to in-cluster resources
by annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/reconcile: disabled
```

When the `kustomize.toolkit.fluxcd.io/reconcile` annotation is set to `disabled`,
the controller will no longer apply changes from source, nor will it prune the resource.
To resume reconciliation, set the annotation to `enabled` or remove it.

Features:
* Allow disabling the reconciliation of in-cluster resources
  [#484](https://github.com/fluxcd/kustomize-controller/pull/484)

Fixes:
* Set delete propagation policy to background
  [#482](https://github.com/fluxcd/kustomize-controller/pull/482)
* Warn when secrets are not decrypted before apply
  [#483](https://github.com/fluxcd/kustomize-controller/pull/483)
* Remove gopass dependency
  [#480](https://github.com/fluxcd/kustomize-controller/pull/480)
* Remove deprecated io/ioutil dependency
  [#479](https://github.com/fluxcd/kustomize-controller/pull/479)

## 0.16.0

**Release date:** 2021-10-19

This prerelease comes with support for SOPS encrypted `.env` files used in kustomize secret generator.

Improvements:
* SOPS: Decrypt dotenv files used in kustomize secret generator
  [#463](https://github.com/fluxcd/kustomize-controller/pull/463)
* SOPS: Document dotenv secret generator
  [#469](https://github.com/fluxcd/kustomize-controller/pull/469)

Fixes:
* Fix cluster scope detection of applied objects
  [#465](https://github.com/fluxcd/kustomize-controller/pull/465)

## 0.15.5

**Release date:** 2021-10-13

This prerelease comes with improvements to drift detection of Kubernetes custom resources.

Improvements:
* Improve drift detection
  [#459](https://github.com/fluxcd/kustomize-controller/pull/459)

## 0.15.4

**Release date:** 2021-10-12

This prerelease comes with fixes to HPA and Service objects validation.

Fixes:
* Fix Service and HPA v2beta1 validation
  [#455](https://github.com/fluxcd/kustomize-controller/pull/455)

## 0.15.3

**Release date:** 2021-10-11

This prerelease comes with fixes for drift detection in Secrets and ConfigMaps.

Fixes:
* Fix drift detection in Secrets and ConfigMaps
  [#451](https://github.com/fluxcd/kustomize-controller/pull/451)

## 0.15.2

**Release date:** 2021-10-10

This prerelease comes with fixes for server-side apply upstream bugs affecting Kubernetes < 1.22.

Fixes:
* Fix SSA upstream bugs for Kubernetes < 1.22
  [#448](https://github.com/fluxcd/kustomize-controller/pull/448)

## 0.15.1

**Release date:** 2021-10-08

This prerelease comes with fixes to backwards compatibility with Flux CLI 0.17 and older.

Fixes:
* Fix inventory panic for v1beta1 objects
  [#445](https://github.com/fluxcd/kustomize-controller/pull/445)

## 0.15.0

**Release date:** 2021-10-08

This prerelease comes with a [new reconciler](https://github.com/fluxcd/kustomize-controller/pull/426)
based on Kubernetes server-side apply and graduates the API to `v1beta2`.

The controller dependencies has been updated to match
kustomize [v4.4.0](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv4.4.0)
which restores the usage of YAML anchors.

**Breaking changes**

- Namespaced objects must contain `metadata.namespace`, defaulting to the `default` namespace is no longer supported.
  Setting a namespace for all objects reconciled by a Kustomization can be done with `spec.targetNamespace`.
- The logs, events and alerts that report Kubernetes namespaced object changes are
  now using the `Kind/Namespace/Name` format instead of `Kind/Name`.
- The minimum required version of Kubernetes has changed to:

    | Kubernetes version | Minimum required |
    | --- | --- |
    | `v1.16` | `>= 1.16.11` |
    | `v1.17` | `>= 1.17.7` |
    | `v1.18` | `>= 1.18.4` |
    | `v1.19` and later | `>= 1.19.0` |

**Features and Improvements**

- Being able to validate and reconcile sources that contain both CRDs and CRs.
- Being able to wait for all the applied resources to become ready
  without requiring users to fill-in the health check list.
- Improve performance (CPU, memory, network, FD usage) and reduce the number of calls to Kubernetes API
  by replacing kubectl execs with a specialized applier written in Go.
- Detect and report drift between the desired state (git, s3, etc) and cluster state reliably.
- Improve the overall observably of the reconciliation process by reporting in real-time
  the garbage collection and health assessment actions.
- Reconcile empty sources including pruning of all the resources previously applied.
- Mask secrets data in logs, events and alerts.

**API changes**

The `kustomize.toolkit.fluxcd.io/v1beta2` API is backwards compatible with `v1beta1`.

Additions, deprecations and removals:
- `.spec.patchesStrategicMerge` deprecated in favour of `.spec.patches`
- `.spec.patchesJson6902` deprecated in favour of `.spec.patches`
- `.spec.validation` deprecated and no longer used (server-side validation is implicit)
- `.spec.wait` added (when enabled, will wait for all the reconciled resources to become ready)
- `.status.snapshot` replaced by `.status.inventory`

Updating the manifests in Git to `v1beta2` can be done at any time after the kustomize-controller upgrade.
All users are encouraged to update the manifests as the deprecated fields
will be removed when the next API version will be released.

## 0.14.1

**Release date:** 2021-09-09

This prerelease comes with improvements to logging.
When Kubernetes Secrets can't be reconciled due to validation errors,
the controller will mask the secret data from logs and events to prevent
disclosing sensitive information.

Improvements:
* Mask the Kubernetes Secrets data from dry-run and apply logs
  [#420](https://github.com/fluxcd/kustomize-controller/pull/420)

## 0.14.0

**Release date:** 2021-08-26

This prerelease comes with improvements to garbage collection.
When pruning is enabled, the controller will skip the deletion of objects with
[ownerReference.BlockOwnerDeletion=true](https://v1-18.docs.kubernetes.io/docs/concepts/workloads/controllers/garbage-collection/#controlling-how-the-garbage-collector-deletes-dependents),
as they are subject to Kubernetes GC.

The controller dependencies has been updated to match
kustomize [v4.3.0](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv4.3.0).

Improvements:
* Update controller to kustomize v4.3.0
  [#416](https://github.com/fluxcd/kustomize-controller/pull/416)
* Skip garbage collection of objects with owner references
  [#411](https://github.com/fluxcd/kustomize-controller/pull/411)
* Add tests for various kustomize transformers
  [#408](https://github.com/fluxcd/kustomize-controller/pull/408)

## 0.13.3

**Release date:** 2021-08-05

This prerelease comes with support for SOPS encrypted kubeconfigs.

Improvements:
* Make the kubeconfig secrets compatible with SOPS
  [#400](https://github.com/fluxcd/kustomize-controller/pull/400)
* Remove old util ObjectKey
  [#397](https://github.com/fluxcd/kustomize-controller/pull/397)
* Var substitution opt-in docs
  [#389](https://github.com/fluxcd/kustomize-controller/pull/389) 
* Update dependencies
  [#401](https://github.com/fluxcd/kustomize-controller/pull/401)

Fixes:
* Prevent nil pointer dereference in health checks
  [#394](https://github.com/fluxcd/kustomize-controller/pull/394)

## 0.13.2

**Release date:** 2021-07-05

This prerelease comes with improvements to health assessment error reporting.

The controller dependencies has been updated to match
kustomize [v4.2.0](https://github.com/kubernetes-sigs/kustomize/releases/tag/kustomize%2Fv4.2.0).

Improvements:
* Make it easier to reason about health check failures
  [#374](https://github.com/fluxcd/kustomize-controller/pull/374)
* Update Alpine v3.14 and kubectl v1.21.2
  [#385](https://github.com/fluxcd/kustomize-controller/pull/385)
* Update controller to kustomize v4.2.0
  [#383](https://github.com/fluxcd/kustomize-controller/pull/383)

Fixes:
* Fix typo in dependency ready log
  [#384](https://github.com/fluxcd/kustomize-controller/pull/384)

## 0.13.1

**Release date:** 2021-06-30

This prerelease comes with kubectl v1.21.1.

Improvements:
* Update kubectl to v1.21.1
  [#381](https://github.com/fluxcd/kustomize-controller/pull/381)
* e2e: Update Kubernetes to v1.21.1
  [#380](https://github.com/fluxcd/kustomize-controller/pull/380)
* Improve test coverage of the `dependsOn` feature
  [#380](https://github.com/fluxcd/kustomize-controller/pull/380)

## 0.13.0

**Release date:** 2021-06-14

This prerelease brings the controller on a par with **Kustomize v4**.
The Kubernetes and controller-runtime dependencies have been updated to match the Kubernetes 1.21 release.

The Kustomization API has been extended with support for generic in-line
[patches](https://github.com/fluxcd/kustomize-controller/blob/v0.13.0/docs/spec/v1beta1/kustomization.md##patches).

Starting with this version, the controller uses an annotation instead of a label
to keep track of removed manifests from source.
Please consult the [garbage collection docs](https://github.com/fluxcd/kustomize-controller/blob/v0.13.0/docs/spec/v1beta1/kustomization.md#garbage-collection)
for more details.

**Breaking changes**:
* Due to the removal of `hashicorp/go-getter` from Kustomize v4,
  the set of URLs accepted by Kustomize in the `resources` filed
  is reduced to only file system paths or values compatible with `git clone`.
  This means you can no longer use resources from archives (zip, tgz, etc).
* YAML anchors are no longer supported in Kustomize v4,
  see [kustomize/issues/3675](https://github.com/kubernetes-sigs/kustomize/issues/3675)
  for more details. 
* Due to a [bug](https://github.com/kubernetes-sigs/kustomize/issues/3446)
  in Kustomize v4, if you have **non-string keys** in your manifests,
  the controller will fail with `json: unsupported type` error.
 
Features:
* Add support for in-line generic patches to Flux Kustomization API
  [#364](https://github.com/fluxcd/kustomize-controller/pull/364)

Improvements:
* Upgrade controller to Kustomize v4
  [#343](https://github.com/fluxcd/kustomize-controller/pull/343)
* Move the GC checksum from labels to annotations
  [#362](https://github.com/fluxcd/kustomize-controller/pull/362)

## 0.12.2

**Release date:** 2021-06-02

This prerelease comes with support for decrypting any file format used with
[Kustomize `secretGenerator`](https://github.com/fluxcd/kustomize-controller/blob/v0.12.2/docs/spec/v1beta1/kustomization.md#kustomize-secretgenerator).

Improvements:
* Support decrypting any file format in secret generator
  [#353](https://github.com/fluxcd/kustomize-controller/pull/353)

## 0.12.1

**Release date:** 2021-05-26

This prerelease comes with a fix to the reconciliation timeout handling.

Improvements:
* Update Go to v1.16
  [#350](https://github.com/fluxcd/kustomize-controller/pull/350)
* Publish on-demand release candidates container images
  [#342](https://github.com/fluxcd/kustomize-controller/pull/342)

Fixes:
* Fix validation and application timeout handling
  [#346](https://github.com/fluxcd/kustomize-controller/pull/346)

## 0.12.0

**Release date:** 2021-04-29

This prerelease comes with support for decrypting Kubernetes
secrets generated with SOPS and
[Kustomize `secretGenerator`](https://github.com/fluxcd/kustomize-controller/blob/v0.12.0/docs/spec/v1beta1/kustomization.md#kustomize-secretgenerator).

Features:
* SOPS: Decrypt Kubernetes secrets generated by kustomize
  [#329](https://github.com/fluxcd/kustomize-controller/pull/329)

Improvements:
* Extract validation error from apply server dry run output
  [#333](https://github.com/fluxcd/kustomize-controller/pull/333)

## 0.11.1

**Release date:** 2021-04-22

This prerelease comes with a bug fix where the rate limited events were
delaying the reconciliation.

Improvements:
* Move GC test to Go test suite
  [#318](https://github.com/fluxcd/kustomize-controller/pull/318)
* Refactor indexers to use high-order functions
  [#322](https://github.com/fluxcd/kustomize-controller/pull/322)

Fixes:
* Avoid retrying rate limited events
  [#326](https://github.com/fluxcd/kustomize-controller/pull/326)
* Make log level info for 'Dependencies do not meet ready condition'
  [#317](https://github.com/fluxcd/kustomize-controller/pull/317)

## 0.11.0

**Release date:** 2021-04-06

This prerelease extends the Mozilla SOPS integration with support for
[age encryption format](https://github.com/fluxcd/kustomize-controller/blob/v0.11.0/docs/spec/v1beta1/kustomization.md#age).

This prerelease comes with a breaking change to SOPS integration.
The OpenPGP private keys stored in Kubernetes secrets must have the `.asc` file extension.
For age, the private keys file extension must be `.agekey`.

Features:
* Support SOPS age encryption
  [#309](https://github.com/fluxcd/kustomize-controller/pull/309)

Improvements:
* Move Kustomize patch tests to Go test suite
  [#313](https://github.com/fluxcd/kustomize-controller/pull/313)
* Detect and replace empty err output on apply
  [#312](https://github.com/fluxcd/kustomize-controller/pull/312)
  
## 0.10.0

**Release date:** 2021-03-26

This prerelease comes with a breaking change to the leader election ID
from `7593cc5d.fluxcd.io` to `kustomize-controller-leader-election`
to be more descriptive. This change should not have an impact on most
installations, as the default replica count is `1`. If you are running
a setup with multiple replicas, it is however advised to scale down
before upgrading.

Improvements:
* Set leader election deadline to 30s
  [#303](https://github.com/fluxcd/kustomize-controller/pull/303)
* Update SOPS to v3.7.0
  [#304](https://github.com/fluxcd/kustomize-controller/pull/304)

## 0.9.3

**Release date:** 2021-03-17

This prerelease comes with updates to the runtime packages.

The controller exposes a gauge metric to track the suspended status
of `Kustomization` objects: `gotk_suspend_status{kind,name,namespace}`.

Improvements:
* Expose suspended status as Prometheus metric
  [#299](https://github.com/fluxcd/kustomize-controller/pull/299)
* Update dependencies
  [#298](https://github.com/fluxcd/kustomize-controller/pull/298)

## 0.9.2

**Release date:** 2021-03-05

This prerelease comes with improvements to the notification system.
The controller retries with exponential backoff when fetching artifacts,
preventing spamming events when source-controller becomes
unavailable for a short period of time.

Improvements:
* Retry with exponential backoff when fetching artifacts
  [#289](https://github.com/fluxcd/kustomize-controller/pull/289)
* Validate the var names before substitution
  [#291](https://github.com/fluxcd/kustomize-controller/pull/291)

## 0.9.1

**Release date:** 2021-02-25

This prerelease comes with an update to the `sigs.k8s.io/cli-utils`
dependency, to guard against a potential bug with health assessments
that was discovered in the `flux` CLI.

Improvements:
* Update sigs.k8s.io/cli-utils to v0.22.2
  [#287](https://github.com/fluxcd/kustomize-controller/pull/287)

## 0.9.0

**Release date:** 2021-02-24

This is the ninth MINOR prerelease.

This prerelease comes with support for recreating Kubernetes objects
(e.g. Jobs) when immutable fields are changed in Git.

Features:
* Add support for recreating objects when immutable fields are updated
  [#271](https://github.com/fluxcd/kustomize-controller/pull/271)

Improvements:
* Extracting validation error from apply dry run output
  [#280](https://github.com/fluxcd/kustomize-controller/pull/280)
* Update kubectl to v1.20.4
  [#283](https://github.com/fluxcd/kustomize-controller/pull/283)

Fixes:
* Avoid prompts on SOPS key import by adding batch flag to gpg
  [#281](https://github.com/fluxcd/kustomize-controller/pull/281)

## 0.8.1

**Release date:** 2021-02-18

This prerelease adds an array field called `substituteFrom` to the post build
API. `SubstituteFrom` holds references to `ConfigMaps` and `Secrets` containing
the variables (data keys) and their values (data values) to be substituted in
the YAML manifests.

You can disable the variable substitution for certain resources by either
labeling or annotating them with:

```yaml
kustomize.toolkit.fluxcd.io/substitute: disabled
``` 

Features:
* Implement var substitution from ConfigMaps and Secrets
  [#275](https://github.com/fluxcd/kustomize-controller/pull/275)

## 0.8.0

**Release date:** 2021-02-12

This is the eight MINOR prerelease.

This prerelease comes with support for bash-style
[variable substitutions](https://github.com/fluxcd/kustomize-controller/blob/v0.8.0/docs/spec/v1beta1/kustomization.md#variable-substitution).

The Kustomization API was extended with in-line support for Kustomize
[Strategic Merge and JSON 6902 patches](https://github.com/fluxcd/kustomize-controller/blob/v0.8.0/docs/spec/v1beta1/kustomization.md#override-kustomize-config).

Pruning can be disabled for certain resources by either
labeling or annotating them with:
```yaml
kustomize.toolkit.fluxcd.io/prune: disabled
```

Golang `pprof` endpoints have been enabled on the metrics server,
making it easier to collect runtime information to debug performance issues.

Features:
* Add support for variable substitutions
  [#253](https://github.com/fluxcd/kustomize-controller/pull/253)
* Support Strategic Merge and JSON 6902 patches
  [#264](https://github.com/fluxcd/kustomize-controller/pull/264)
* Allow disabling of prune on certain resources
  [#267](https://github.com/fluxcd/kustomize-controller/pull/267)

Improvements:
* Enable pprof endpoints on metrics server
  [#266](https://github.com/fluxcd/kustomize-controller/pull/266)
* Update `kustomize/api` to v0.7.4
  [#269](https://github.com/fluxcd/kustomize-controller/pull/269)

## 0.7.4

**Release date:** 2021-02-02

This prerelease comes with a change to the Kustomization
status so that the controller can keep track of the last health
assessment result and avoid issuing redundant health check events.

Improvements:
* Add Healthy status condition
  [#262](https://github.com/fluxcd/kustomize-controller/pull/262)
  
## 0.7.3

**Release date:** 2021-02-01

This prerelease comes with support for running the
Kustomization validation using service account impersonation.

Improvements:
* Support impersonation for validation
  [#260](https://github.com/fluxcd/kustomize-controller/pull/260)

## 0.7.2

**Release date:** 2021-01-26

This prerelease comes with a fix to the service account impersonation
when running health checks and garbage collection.

Fixes:
* Clear `config.BearerTokenFile` when setting `BearerToken`
  [#258](https://github.com/fluxcd/kustomize-controller/pull/258)

## 0.7.1

**Release date:** 2021-01-25

This prerelease fixes a regression bug introduced in `v0.7.0` that caused
failed reconciliations to be immediately retried instead of being scheduled
at the specified interval.

Fixes:
* Fix reconciliation retry scheduler
  [#256](https://github.com/fluxcd/kustomize-controller/pull/256)

## 0.7.0

**Release date:** 2021-01-22

This is the seventh MINOR prerelease.

An optional field was added `spec.retryInterval` that allows users to
requeue a failed reconciliation at a different interval than `spec.Interval`.

The `LocalObjectReference` from the Kubernetes core has been replaced
with our own, making `Name` a required field. The impact of this should
be limited to direct API consumers only, as the field was already
required by controller logic.

Improvements:
* Allow failed reconciliations to be scheduled at a different interval
  [#250](https://github.com/fluxcd/kustomize-controller/pull/250)
* Update fluxcd/pkg/runtime to v0.8.0
  [#247](https://github.com/fluxcd/kustomize-controller/pull/247)


## 0.6.3

**Release date:** 2021-01-19

This prerelease comes with fixes to the kustomize build procedure by disabling kyaml.
The Kubernetes packages were updated to v1.20.2 and kustomize/api to v0.7.2.

Improvements:
* Update kustomize/api to v0.7.2 and disable kyaml
  [#241](https://github.com/fluxcd/kustomize-controller/pull/241)
* Update Kubernetes packages to v1.20.2
  [#240](https://github.com/fluxcd/kustomize-controller/pull/240)

## 0.6.2

**Release date:** 2021-01-15

This prerelease adds support for `kustomization.yml` and `Kustomization`
files at the root of the configured path.

Improvements:
* Look for all accepted Kustomization filenames
  [#238](https://github.com/fluxcd/kustomize-controller/pull/238)

## 0.6.1

**Release date:** 2021-01-14

This prerelease fixes a regression bug introduced in `v0.6.0` that caused
reconciliation request annotations to be ignored in certain scenarios.

Two new argument flags are introduced to support configuring the QPS 
(`--kube-api-qps`) and burst (`--kube-api-burst`) while communicating
with the Kubernetes API server.

Improvements:
* Add support for QPS and burst configurations
  [#233](https://github.com/fluxcd/kustomize-controller/pull/233)
* Update source-controller to v0.6.1
  [#234](https://github.com/fluxcd/kustomize-controller/pull/234)

Fixes:
* Upgrade runtime package to v0.6.2 for regression bug fix
  [#234](https://github.com/fluxcd/kustomize-controller/pull/234)

## 0.6.0

**Release date:** 2021-01-12

This is the sixth MINOR prerelease, upgrading the `controller-runtime`
dependencies to `v0.7.0`.

The container image for ARMv7 and ARM64 that used to be published
separately as `kustomize-controller:*-arm64` has been merged with the
AMD64 image.

Improvements:
* Update kubectl to v1.20.1
  [#231](https://github.com/fluxcd/kustomize-controller/pull/231)
* Update kustomize/api to v0.7.1
  [#229](https://github.com/fluxcd/kustomize-controller/pull/229)
* Run GC and health checks using service account impersonation
  [#221](https://github.com/fluxcd/kustomize-controller/pull/221)

Fixes:
* No longer treat dirs as kustomization files
  [#224](https://github.com/fluxcd/kustomize-controller/pull/224)
  
## 0.5.3

**Release date:** 2020-12-18

This prerelease comes with improvements to health reporting.

Improvements:
* Emit healthcheck event when Kustomization was not ready
  [#219](https://github.com/fluxcd/kustomize-controller/pull/219)
* Add debug logs to garbage collection
  [#218](https://github.com/fluxcd/kustomize-controller/pull/218)
* Add SOPS user error to logs
  [#220](https://github.com/fluxcd/kustomize-controller/pull/220)

## 0.5.2

**Release date:** 2020-12-16

This prerelease comes with improvements to garbage collection
and adds safe guards for relative paths. 

The `Path` field was marked as optional, when not specified,
it defaults to the root path of the `SourceRef`.

Health checking and garbage collection for `HelmReleases`
and other custom resources were fixed by downgrading 
to `controller-runtime` v0.6.3.

Improvements:
* Refactor garbage collection
  [#210](https://github.com/fluxcd/kustomize-controller/pull/210)
* Make Path field optional and add safe guards for relative paths
  [#211](https://github.com/fluxcd/kustomize-controller/pull/211)

## 0.5.1

**Release date:** 2020-12-14

This prerelease comes with improvements to error reporting.

Improvements:
* Refactor apply error reporting
  [#205](https://github.com/fluxcd/kustomize-controller/pull/205)

## 0.5.0

**Release date:** 2020-12-11

This is the fifth MINOR prerelease. It comes with support for
overriding container images with `spec.images`.

Improvements:
* Implement container image setters
    [#194](https://github.com/fluxcd/kustomize-controller/pull/194)
* Update kustomize/api to v0.7.0 
    [#198](https://github.com/fluxcd/kustomize-controller/pull/198)

## 0.4.0

**Release date:** 2020-11-26

This is the fourth MINOR prerelease. Suspended Kustomizations are
no longer marked as not ready, the status sub-resource is left untouched. 

Improvements:
* Do not mark suspended resource as not ready
    [#183](https://github.com/fluxcd/kustomize-controller/pull/183)
* Set field manager to `kustomize-controller` instead of `kubectl`
    [#184](https://github.com/fluxcd/kustomize-controller/pull/184)

Fixes:
* Requeue after interval on source not found errors
    [#182](https://github.com/fluxcd/kustomize-controller/pull/182)

## 0.3.0

**Release date:** 2020-11-20

This is the third MINOR prerelease. It introduces two breaking changes:
* the `ServiceAccount` field has been removed and replaced by `ServiceAccountName`;
    it is no longer possible to specify a namespace for a service account,
    the namespace is inferred from the Kustomization namespace 
* the status condition type has changed to the type introduced in Kubernetes API machinery `v1.19.0`

Improvements:
* Use ServiceAccountName for impersonation
    [#180](https://github.com/fluxcd/kustomize-controller/pull/180)
* Adopt Kubernetes condition type 
    [#174](https://github.com/fluxcd/kustomize-controller/pull/174)
* Add docs for excluding non-k8 YAML files in kustomization generation
    [#176](https://github.com/fluxcd/kustomize-controller/pull/176)
* Use DeletionTimestamp for prune and readiness
    [#177](https://github.com/fluxcd/kustomize-controller/pull/177)

Fixes:
* Add fsGroup to security context (fix for AWS KMS IAM Role bindings)
    [#178](https://github.com/fluxcd/kustomize-controller/pull/178)

## 0.2.2

**Release date:** 2020-11-12

This prerelease comes with improvements to status reporting.
The Kustomization dry-run can now be explicitly disabled
by setting `spec.validation` to `none`.

Improvements:
* Patch status sub-resource
    [#171](https://github.com/fluxcd/kustomize-controller/pull/171)
* Update kustomize/api to v0.6.5
    [#170](https://github.com/fluxcd/kustomize-controller/pull/170)
* Allow disabling validation
    [#168](https://github.com/fluxcd/kustomize-controller/pull/168)

## 0.2.1

**Release date:** 2020-11-04

This prerelease comes with improvements to garbage collection.
The Kubernetes packages have been updated to v1.19.

Improvements:
* Update k8s to 1.19 + kustomize 0.6.4
    [#161](https://github.com/fluxcd/kustomize-controller/pull/161)
* Add openssh client to support git+ssh Kustomize resources
    [#159](https://github.com/fluxcd/kustomize-controller/pull/159)

Fixes:
* Use latest generation when updating final status
    [#164](https://github.com/fluxcd/kustomize-controller/pull/164)
* Omit checksum label if GC is disabled
    [#162](https://github.com/fluxcd/kustomize-controller/pull/162)

## 0.2.0

**Release date:** 2020-10-29

This is the second MINOR prerelease, it comes with breaking changes:
* the histogram metric `gotk_reconcile_duration` was renamed to `gotk_reconcile_duration_seconds`
* the annotation `fluxcd.io/reconcileAt` was renamed to `reconcile.fluxcd.io/requestedAt`

Improvements:
* Refactor predicates and enqueuers
    [#156](https://github.com/fluxcd/kustomize-controller/pull/156)
    [#155](https://github.com/fluxcd/kustomize-controller/pull/155)
    [#153](https://github.com/fluxcd/kustomize-controller/pull/153)
* Use annotation helpers
    [#152](https://github.com/fluxcd/kustomize-controller/pull/152)
* Suppress health check events when no changes made
    [#151](https://github.com/fluxcd/kustomize-controller/pull/151)
* Use controller-runtime utils for finalizer and health checks
    [#150](https://github.com/fluxcd/kustomize-controller/pull/150)
* Improve remote cluster documentation
    [#148](https://github.com/fluxcd/kustomize-controller/pull/148)

## 0.1.2

**Release date:** 2020-10-16

This prerelease comes with support for
[targeting remote clusters](https://github.com/fluxcd/kustomize-controller/blob/main/docs/spec/v1beta1/kustomization.md#cluster-api--remote-clusters)
created with Cluster-API.

Improvements:
* Implement non-caching, per-kustomization GC-client/statusPoller for cross-cluster kubeconfigs
    [#135](https://github.com/fluxcd/kustomize-controller/pull/135)

Fixes:
* Fix status reporting when the source is not found
    [#141](https://github.com/fluxcd/kustomize-controller/pull/141)
* Validate manifests when generating kustomization.yaml
    [#143](https://github.com/fluxcd/kustomize-controller/pull/143)
* Set correct status on failure events
    [#145](https://github.com/fluxcd/kustomize-controller/pull/145)

## 0.1.1

**Release date:** 2020-10-13

This prerelease comes with Prometheus instrumentation for the controller's resources.

For each kind, the controller exposes a gauge metric to track the `Ready` condition status,
and a histogram with the reconciliation duration in seconds:

* `gotk_reconcile_condition{kind, name, namespace, status, type="Ready"}`
* `gotk_reconcile_duration{kind, name, namespace}`

## 0.1.0

**Release date:** 2020-09-30

This is the first MINOR prerelease, it promotes the
`kustomize.toolkit.fluxcd.io` API to `v1beta1`
and removes support for `v1alpha1`.

Going forward, changes to the API will be accompanied by a conversion
mechanism. With this release the API becomes more stable, but while in
beta phase there are no guarantees about backwards compatibility
between beta releases.

## 0.0.13

**Release date:** 2020-09-22

This prerelease comes with support for S3 bucket sources and
cross-namespace dependencies.
Container images for ARMv7 and ARMv8 are published to
`ghcr.io/fluxcd/kustomize-controller-arm64`.

## 0.0.12

**Release date:** 2020-09-14

This prerelease comes with improvements to health assessment.
The health checks leverage the kstatus library to support
custom resources such as HelmReleases that implement the Ready condition.

## 0.0.11

**Release date:** 2020-09-12

This prerelease comes with the option to watch for resources
in the runtime namespace of the controller or at cluster level.

## 0.0.10

**Release date:** 2020-09-05

This prerelease comes with a bug fix to garbage collection.

## 0.0.9

**Release date:** 2020-09-04

This prerelease comes with support for decrypting Kubernetes secrets
with Mozilla SOPS.
Container images for linux/amd64 and linux/arm64 are published to GHCR.

## 0.0.8

**Release date:** 2020-08-18

This prerelease upgrades the `github.com/fluxcd/pkg/*` dependencies to
dedicated versioned modules, and makes the `api` package available as
a dedicated versioned module.

## 0.0.7

**Release date:** 2020-07-31

This prerelease comes with a breaking change, the CRDs group has been
renamed to `kustomize.toolkit.fluxcd.io`. The dependency on `source-controller`
has been updated to `v0.0.7` to be able to work with `source.toolkit.fluxcd.io`
resources.

## 0.0.6

**Release date:** 2020-07-25

This prerelease comes with improvements to the dependency management
and service account impersonation.

## 0.0.5

**Release date:** 2020-07-20

This prerelease drops support for Kubernetes <1.16.
The CRDs have been updated to `apiextensions.k8s.io/v1`.

## 0.0.4

**Release date:** 2020-07-16

This prerelease comes with improvements to the alerting system
and allows sources to be referenced across namespaces.
The kustomize/api has been updated to v0.5.1.

## 0.0.3

**Release date:** 2020-07-13

This prerelease comes with improvements to logging.
The default logging format is JSON and the timestamp format is ISO8601.
Introduce `fluxcd.io/reconcileAt` annotation for on-demand reconciliation
of kustomization objects.

## 0.0.2

**Release date:** 2020-07-02

This prerelease comes with improvements to the alerting system.
The reconciliation events can be forwarded to notification controller
and alerting can be configured for Slack, MS Teams, Discord and Rocket chat
using the [notification.fluxcd.io API](https://github.com/fluxcd/notification-controller/tree/main/docs/spec).

## 0.0.1

**Release date:** 2020-06-24

This is the first prerelease ready for public testing. To get started
testing, see the [GitOps Toolkit guide](https://fluxcd.io/flux/get-started/).

## 0.0.1-beta.2

**Release date:** 2020-06-10

This beta release allows configuring the number of concurrent reconciles.
Starting with this version, the controller watches for resources
only in the namespace where it's deployed.

## 0.0.1-beta.1

**Release date:** 2020-05-29

This is the first beta release of kustomize controller.
This release comes with improvements to the reconciliation engine when 
dealing with CRDs/CRs. The kustomize/api has been updated to v0.4.1.

## 0.0.1-alpha.9

**Release date:** 2020-05-11

This alpha release includes a bug fix for the source event handler
and sets the current context to the default namespace.

## 0.0.1-alpha.8

**Release date:** 2020-05-09

This alpha release comes with improvements to health assessment
and [dependency management](https://github.com/fluxcd/kustomize-controller/pull/32).
When a source revision changes, the
Kustomizations are executed based on the depends-on graph.

## 0.0.1-alpha.7

**Release date:** 2020-05-05

This alpha release comes with improvements to the garbage collector.
The new GC doesn't require label selectors to be set in the kustomization
and can prune resources safely without hitting Kubernetes API rate limits.

## 0.0.1-alpha.6

**Release date:** 2020-05-03

This alpha release comes with
[role-based access control](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.6/docs/spec/v1alpha1/kustomization.md#role-based-access-control)
for restricting the execution of a kustomization apply to a specific service account.

## 0.0.1-alpha.5

**Release date:** 2020-04-27

This alpha release introduces an [intermediate state](https://github.com/fluxcd/kustomize-controller/pull/21)
to the status ready condition to signal that a reconciliation is underway.
This allows waiting for an on-demand sync to complete.

## 0.0.1-alpha.4

**Release date:** 2020-04-24

This alpha release introduces a new status field for recording the
[last applied source revision](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.4/docs/spec/v1alpha1/kustomization.md#status).

Feature comparison with Flux has been added to
[docs/spec](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.4/docs/spec/README.md#backward-compatibility).

## 0.0.1-alpha.3

**Release date:** 2020-04-23

This alpha release introduces the option to tell the controller to
[automatically generate](https://github.com/fluxcd/kustomize-controller/blob/v0.0.1-alpha.3/docs/spec/v1alpha1/kustomization.md#generate-kustomizationyaml)
the `kustomization.yaml` for repositories that contain plain Kubernetes manifests.

The controller design and motivation can be found at
[docs/spec](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.3/docs/spec).

## 0.0.1-alpha.2

**Release date:** 2020-04-21

This alpha release introduces the
[Profile CRD](https://github.com/fluxcd/kustomize-controller/blob/main/docs/spec/v1alpha1/profile.md)
that allows grouping
[Kustomization](https://github.com/fluxcd/kustomize-controller/blob/main/docs/spec/v1alpha1/kustomization.md)
objects and defining a common behavior for them.
The v1alpha1 profiles can be used for
[configuring Slack and Discord alerting](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.2#configure-alerting).

## 0.0.1-alpha.1

**Release date:** 2020-04-20

This is the first alpha release of kustomize controller.
The controller is an implementation of the
[kustomize.fluxcd.io/v1alpha1](https://github.com/fluxcd/kustomize-controller/tree/v0.0.1-alpha.1/docs/spec/v1alpha1) API.
