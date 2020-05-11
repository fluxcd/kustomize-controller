module github.com/fluxcd/kustomize-controller

go 1.13

require (
	github.com/fluxcd/source-controller v0.0.1-alpha.6
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	k8s.io/api v0.17.2
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
	sigs.k8s.io/controller-runtime v0.5.0
	sigs.k8s.io/kustomize/api v0.3.3
	sigs.k8s.io/yaml v1.2.0
)
