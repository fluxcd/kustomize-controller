module github.com/fluxcd/kustomize-controller

go 1.16

replace github.com/fluxcd/kustomize-controller/api => ./api

require (
	filippo.io/age v1.0.0
	github.com/cyphar/filepath-securejoin v0.2.2
	github.com/drone/envsubst v1.0.3-0.20200804185402-58bc65f69603
	github.com/fluxcd/kustomize-controller/api v0.18.0
	github.com/fluxcd/pkg/apis/kustomize v0.2.0
	github.com/fluxcd/pkg/apis/meta v0.10.1
	github.com/fluxcd/pkg/runtime v0.12.2
	github.com/fluxcd/pkg/ssa v0.3.1
	github.com/fluxcd/pkg/testserver v0.1.0
	github.com/fluxcd/pkg/untar v0.1.0
	github.com/fluxcd/source-controller/api v0.18.0
	github.com/go-logr/logr v0.4.0
	github.com/hashicorp/go-retryablehttp v0.7.0
	github.com/onsi/gomega v1.15.0
	github.com/spf13/pflag v1.0.5
	go.mozilla.org/sops/v3 v3.7.1
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5
	golang.org/x/net v0.0.0-20210520170846-37e1c6afe023
	google.golang.org/grpc v1.42.0
	k8s.io/api v0.22.2
	k8s.io/apiextensions-apiserver v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.22.2
	sigs.k8s.io/cli-utils v0.26.0
	sigs.k8s.io/controller-runtime v0.10.2
	sigs.k8s.io/kustomize/api v0.10.1
	sigs.k8s.io/yaml v1.3.0
)

// pin kustomize to v4.4.1
replace (
	sigs.k8s.io/kustomize/api => sigs.k8s.io/kustomize/api v0.10.1
	sigs.k8s.io/kustomize/kyaml => sigs.k8s.io/kustomize/kyaml v0.13.0
)

// fix CVE-2021-30465
replace github.com/opencontainers/runc => github.com/opencontainers/runc v1.0.2

// fix CVE-2021-41190
replace github.com/opencontainers/image-spec => github.com/opencontainers/image-spec v1.0.2
