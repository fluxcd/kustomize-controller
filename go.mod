module github.com/fluxcd/kustomize-controller

go 1.15

replace github.com/fluxcd/kustomize-controller/api => ./api

require (
	filippo.io/age v1.0.0-beta7
	github.com/Microsoft/go-winio v0.4.16 // indirect
	github.com/cyphar/filepath-securejoin v0.2.2
	github.com/drone/envsubst v1.0.3-0.20200804185402-58bc65f69603
	github.com/fluxcd/kustomize-controller/api v0.12.0
	github.com/fluxcd/pkg/apis/kustomize v0.0.1
	github.com/fluxcd/pkg/apis/meta v0.9.0
	github.com/fluxcd/pkg/runtime v0.11.0
	github.com/fluxcd/pkg/testserver v0.0.2
	github.com/fluxcd/pkg/untar v0.0.5
	github.com/fluxcd/source-controller/api v0.12.1
	github.com/go-logr/logr v0.4.0
	github.com/hashicorp/go-multierror v1.1.0 // indirect
	github.com/hashicorp/go-retryablehttp v0.6.8
	github.com/howeyc/gopass v0.0.0-20170109162249-bf9dde6d0d2c
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/mattn/go-colorable v0.1.4 // indirect
	github.com/onsi/ginkgo v1.16.2
	github.com/onsi/gomega v1.12.0
	github.com/spf13/pflag v1.0.5
	go.mozilla.org/gopgagent v0.0.0-20170926210634-4d7ea76ff71a
	go.mozilla.org/sops/v3 v3.7.1
	golang.org/x/crypto v0.0.0-20210322153248-0c34fe9e7dc2
	golang.org/x/net v0.0.0-20210428140749-89ef3d95e781
	google.golang.org/grpc v1.27.1
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	k8s.io/api v0.21.1
	k8s.io/apiextensions-apiserver v0.21.1
	k8s.io/apimachinery v0.21.1
	k8s.io/client-go v0.21.1
	sigs.k8s.io/cli-utils v0.25.0
	sigs.k8s.io/controller-runtime v0.9.0-beta.5.0.20210524185538-7181f1162e79
	sigs.k8s.io/kustomize/api v0.8.10
	sigs.k8s.io/yaml v1.2.0
)

// break away from cli-utils <-> kubectl <-> kustomize cyclic dependency nighmare
//+-> sigs.k8s.io/cli-utils@v0.25.0
//	+-> k8s.io/kubectl@v0.20.4
//		+-> k8s.io/kube-openapi@v0.0.0-20201113171705-d219536bb9fd
//		+-> sigs.k8s.io/kustomize@v2.0.3+incompatible
//	+-> sigs.k8s.io/kustomize/kyaml@v0.10.16
//		+-> k8s.io/kube-openapi@v0.0.0-20210421082810-95288971da7e
// Remove pin when this PR is merged: https://github.com/kubernetes-sigs/cli-utils/pull/361
replace sigs.k8s.io/cli-utils => github.com/ash2k/cli-utils v0.15.1-0.20210526122353-4f9bdf148ae9

// breaking change in k8s.io/client-go/tools/metrics@0.21.1
// *resultAdapter does not implement metrics.ResultMetric (wrong type for Increment method)
//   have Increment(string, string, string)
//   want Increment(context.Context, string, string, string)
replace sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.9.0-beta.5

// pin kustomize to v4.1.3
replace (
	sigs.k8s.io/kustomize/api => sigs.k8s.io/kustomize/api v0.8.10
	sigs.k8s.io/kustomize/kyaml => sigs.k8s.io/kustomize/kyaml v0.10.20
)
