module github.com/fluxcd/kustomize-controller/api

go 1.15

require (
	github.com/fluxcd/pkg/apis/meta v0.5.0
	github.com/fluxcd/pkg/runtime v0.5.1-0.20201217125143-9e1fe564d778
	k8s.io/api v0.19.4
	k8s.io/apimachinery v0.19.4
	sigs.k8s.io/controller-runtime v0.7.0
)
