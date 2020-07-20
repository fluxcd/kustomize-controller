module github.com/fluxcd/kustomize-controller

go 1.14

require (
	github.com/fluxcd/pkg v0.0.3
	github.com/fluxcd/source-controller v0.0.6
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	go.uber.org/zap v1.10.0
	k8s.io/api v0.18.4
	k8s.io/apimachinery v0.18.4
	k8s.io/client-go v0.18.4
	sigs.k8s.io/controller-runtime v0.6.1
	sigs.k8s.io/kustomize/api v0.5.1
	sigs.k8s.io/yaml v1.2.0
)

// TODO: drop when PR is accepted:
//  https://github.com/sosedoff/gitkit/pull/21
replace github.com/sosedoff/gitkit => github.com/hiddeco/gitkit v0.2.1-0.20200422093229-4355fec70348
