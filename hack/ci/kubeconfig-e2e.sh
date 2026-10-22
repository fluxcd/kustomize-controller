#!/usr/bin/env bash
# End-to-end test for .spec.kubeConfig.configMapRef between two kind clusters.
#
# The primary cluster runs the Flux controllers and the remote cluster is the
# target. The same remote cluster trusts both the Kubernetes ServiceAccount
# issuer from the primary cluster and a SPIFFE OIDC issuer backed by the SPIFFE
# Broker API. Two tenants assert that each identity can access its own namespace
# but not the other tenant's namespace.
set -euo pipefail

IMG="${IMG:?IMG must be set}"
CLUSTER="${CLUSTER:-kind}"
REMOTE_CLUSTER="${REMOTE_CLUSTER:-kubeconfig-remote}"
REGISTRY_NAME="kubeconfig-registry"
REGISTRY_PORT="${REGISTRY_PORT:-5001}"
REGISTRY_IMAGE="${REGISTRY_IMAGE:-registry:2}"
APP_NS="kubeconfig-e2e"
REV="v1"
KUBECONFIG_AUDIENCE="kubeconfig-e2e"
SPIFFE_TRUST_DOMAIN="example.org"
SPIFFE_OIDC_PORT="8083"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKDIR="$(mktemp -d)"
CREATED_REMOTE=false

NS=kustomize-system
CONTROLLER=kustomize-controller
JWT_OBJ_TYPE=kustomizations.kustomize.toolkit.fluxcd.io

log()  { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
    local code=$?
    if [ "${code}" -ne 0 ]; then
        log "FAILURE (exit ${code}) — dumping diagnostics"
        kubectl --context "kind-${CLUSTER}" -n "${NS}" logs "deploy/${CONTROLLER}" --tail=120 2>/dev/null || true
        kubectl --context "kind-${CLUSTER}" -n "${APP_NS}" get kustomizations -o yaml 2>/dev/null || true
        kubectl --context "kind-${REMOTE_CLUSTER}" get all -A 2>/dev/null || true
    fi
    if [ -n "${KEEP:-}" ]; then
        log "KEEP set — leaving clusters and ${WORKDIR} for inspection"
        exit "${code}"
    fi
    docker rm -f "${REGISTRY_NAME}" >/dev/null 2>&1 || true
    if [ "${CREATED_REMOTE}" = "true" ]; then
        kind delete cluster --name "${REMOTE_CLUSTER}" >/dev/null 2>&1 || true
    fi
    rm -rf "${WORKDIR}"
    exit "${code}"
}
trap cleanup EXIT

tools=(docker kind kubectl kustomize flux openssl)
for bin in "${tools[@]}"; do
    command -v "${bin}" >/dev/null 2>&1 || fail "required tool '${bin}' not found"
done

source "${ROOT_DIR}/hack/ci/spire-broker.sh"

kubectl config use-context "kind-${CLUSTER}" >/dev/null
kubectl wait --for=condition=Ready nodes --all --timeout=2m

log "Deploying ${CONTROLLER} in the primary cluster"
kind load docker-image "${IMG}" --name "${CLUSTER}"
make -C "${ROOT_DIR}" dev-deploy IMG="${IMG}" >/dev/null
kubectl -n "${NS}" rollout status "deploy/source-controller" --timeout=2m
kubectl -n "${NS}" rollout status "deploy/${CONTROLLER}" --timeout=2m

log "Preparing the primary cluster OIDC discovery"
kubectl create clusterrolebinding oidc-reviewer \
    --clusterrole=system:service-account-issuer-discovery \
    --group=system:unauthenticated >/dev/null 2>&1 || true

##############################################################################
log "Generating a TLS certificate for the SPIFFE OIDC provider"
##############################################################################
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -subj "/CN=kubeconfig-oidc-ca" \
    -keyout "${WORKDIR}/oidc-ca.key" \
    -out "${WORKDIR}/oidc-ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
    -subj "/CN=${CLUSTER}-control-plane" \
    -keyout "${WORKDIR}/oidc-tls.key" \
    -out "${WORKDIR}/oidc-tls.csr" >/dev/null 2>&1
cat > "${WORKDIR}/oidc-tls.ext" <<EOF
subjectAltName=DNS:${CLUSTER}-control-plane
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -days 1 \
    -in "${WORKDIR}/oidc-tls.csr" \
    -CA "${WORKDIR}/oidc-ca.crt" \
    -CAkey "${WORKDIR}/oidc-ca.key" \
    -CAcreateserial \
    -extfile "${WORKDIR}/oidc-tls.ext" \
    -out "${WORKDIR}/oidc-tls.crt" >/dev/null 2>&1

kubectl create namespace spire --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n spire create secret tls spire-oidc-tls \
    --cert="${WORKDIR}/oidc-tls.crt" \
    --key="${WORKDIR}/oidc-tls.key" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

##############################################################################
log "Installing SPIRE with the Broker API"
##############################################################################
SPIRE_NAMESPACE=spire
SPIRE_TRUST_DOMAIN="${SPIFFE_TRUST_DOMAIN}"
SPIRE_SERVER_IMAGE="${SPIRE_SERVER_IMAGE:-ghcr.io/spiffe/spire-server:1.15.5}"
SPIRE_AGENT_IMAGE="${SPIRE_AGENT_IMAGE:-ghcr.io/spiffe/spire-agent:1.15.5}"
SPIRE_OIDC_IMAGE="${SPIRE_OIDC_IMAGE:-ghcr.io/spiffe/oidc-discovery-provider:1.15.5}"
SPIRE_CLUSTER="${CLUSTER}"
SPIRE_OIDC_ISSUER="http://${CLUSTER}-control-plane:8082"
SPIRE_JWT_ISSUER="https://${CLUSTER}-control-plane:${SPIFFE_OIDC_PORT}"
SPIRE_BROKER_ENDPOINT="dns:///spire-broker.spire:8443"
SPIRE_BROKER_ID="spiffe://${SPIRE_TRUST_DOMAIN}/spire/agent/k8s_psat/broker-cluster/"
BROKER_SA="$(kubectl -n "${NS}" get deploy "${CONTROLLER}" -o jsonpath='{.spec.template.spec.serviceAccountName}')"
[ -n "${BROKER_SA}" ] || BROKER_SA=default
JWT_OBJ_NAME="kc-spiffe-a-own"
JWT_SPIFFE_ID="spiffe://${SPIRE_TRUST_DOMAIN}/kubeconfig/${JWT_OBJ_NAME}"
install_spire_broker

log "Deploying the HTTPS SPIFFE OIDC provider"
cat > "${WORKDIR}/spire-oidc-tls.conf" <<EOF
log_level = "INFO"
domains = ["${CLUSTER}-control-plane"]
jwt_issuer = "${SPIRE_JWT_ISSUER}"
jwks_uri = "${SPIRE_JWT_ISSUER}/keys"
serving_cert_file {
  cert_file_path = "/run/spire/oidc-tls/tls.crt"
  key_file_path = "/run/spire/oidc-tls/tls.key"
  addr = "0.0.0.0:${SPIFFE_OIDC_PORT}"
}
workload_api {
  socket_path = "/run/spire/agent-sockets/api.sock"
  trust_domain = "${SPIRE_TRUST_DOMAIN}"
}
EOF
kubectl -n spire create configmap spire-oidc-tls \
    --from-file=oidc.conf="${WORKDIR}/spire-oidc-tls.conf" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n spire apply -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: spire-oidc-tls
  namespace: spire
  labels:
    app: spire-oidc-tls
spec:
  replicas: 1
  selector:
    matchLabels:
      app: spire-oidc-tls
  template:
    metadata:
      labels:
        app: spire-oidc-tls
    spec:
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      serviceAccountName: spire-agent
      containers:
        - name: oidc-discovery-provider
          image: ${SPIRE_OIDC_IMAGE}
          args: ["-config", "/run/spire/oidc/oidc.conf"]
          ports:
            - name: oidc
              containerPort: ${SPIFFE_OIDC_PORT}
          readinessProbe:
            httpGet:
              scheme: HTTPS
              path: /.well-known/openid-configuration
              port: ${SPIFFE_OIDC_PORT}
              httpHeaders:
                - name: Host
                  value: ${CLUSTER}-control-plane
            initialDelaySeconds: 5
            periodSeconds: 5
          volumeMounts:
            - name: spire-oidc-tls
              mountPath: /run/spire/oidc
              readOnly: true
            - name: spire-oidc-tls-cert
              mountPath: /run/spire/oidc-tls
              readOnly: true
            - name: spire-agent-socket
              mountPath: /run/spire/agent-sockets
      volumes:
        - name: spire-oidc-tls
          configMap:
            name: spire-oidc-tls
        - name: spire-oidc-tls-cert
          secret:
            secretName: spire-oidc-tls
        - name: spire-agent-socket
          hostPath:
            path: /run/spire/agent-sockets
            type: DirectoryOrCreate
EOF
kubectl -n spire rollout status deploy/spire-oidc-tls --timeout=2m

# Register the remaining SPIFFE identities used by this test.
object_names=(kc-spiffe-a-own kc-spiffe-a-cross kc-spiffe-b-own kc-spiffe-b-cross)
for name in "${object_names[@]}"; do
    [ "${name}" = "${JWT_OBJ_NAME}" ] && continue
    kubectl -n spire exec deployment/spire-server -- \
        /opt/spire/bin/spire-server entry create \
        -spiffeID "spiffe://${SPIRE_TRUST_DOMAIN}/kubeconfig/${name}" \
        -parentID "${AGENT_ID}" \
        -selector "k8s:resource:${JWT_OBJ_TYPE}" \
        -selector "k8s:namespace:${APP_NS}" \
        -selector "k8s:name:${name}" >/dev/null
done

##############################################################################
log "Creating the remote kind cluster with both JWT authenticators"
##############################################################################
docker cp "${CLUSTER}-control-plane:/etc/kubernetes/pki/ca.crt" "${WORKDIR}/primary-ca.crt"
cat > "${WORKDIR}/authentication.yaml" <<EOF
apiVersion: apiserver.config.k8s.io/v1
kind: AuthenticationConfiguration
jwt:
  - issuer:
      url: "https://${CLUSTER}-control-plane:6443"
      certificateAuthority: |
$(sed 's/^/        /' "${WORKDIR}/primary-ca.crt")
      audiences: ["${KUBECONFIG_AUDIENCE}"]
    claimMappings:
      username:
        claim: sub
        prefix: ""
  - issuer:
      url: "${SPIRE_JWT_ISSUER}"
      certificateAuthority: |
$(sed 's/^/        /' "${WORKDIR}/oidc-ca.crt")
      audiences: ["${KUBECONFIG_AUDIENCE}"]
    claimMappings:
      username:
        claim: sub
        prefix: ""
EOF
mkdir -p "${WORKDIR}/authentication"
mv "${WORKDIR}/authentication.yaml" "${WORKDIR}/authentication/config.yaml"
cat > "${WORKDIR}/remote-kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraMounts:
      - hostPath: ${WORKDIR}/authentication
        containerPath: /etc/kubernetes/authentication
        readOnly: true
    kubeadmConfigPatches:
      - |
        kind: ClusterConfiguration
        apiServer:
          extraArgs:
            authentication-config: /etc/kubernetes/authentication/config.yaml
          extraVolumes:
            - name: authentication-config
              hostPath: /etc/kubernetes/authentication
              mountPath: /etc/kubernetes/authentication
              readOnly: true
EOF
kind delete cluster --name "${REMOTE_CLUSTER}" >/dev/null 2>&1 || true
kind create cluster --name "${REMOTE_CLUSTER}" --config "${WORKDIR}/remote-kind.yaml" --wait 3m
CREATED_REMOTE=true
kubectl config use-context "kind-${REMOTE_CLUSTER}" >/dev/null
docker cp "${REMOTE_CLUSTER}-control-plane:/etc/kubernetes/pki/ca.crt" "${WORKDIR}/remote-ca.crt"

##############################################################################
log "Creating remote tenant namespaces and RBAC"
##############################################################################
kubectl create namespace tenant-a --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create namespace tenant-b --dry-run=client -o yaml | kubectl apply -f - >/dev/null
for tenant in a b; do
    kubectl -n "tenant-${tenant}" apply -f - >/dev/null <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: deployer
rules:
  - apiGroups: ["", "apps", "batch", "policy", "autoscaling", "networking.k8s.io"]
    resources: ["*"]
    verbs: ["*"]
EOF
done

kubectl config use-context "kind-${CLUSTER}" >/dev/null
kubectl create namespace "${APP_NS}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

for tenant in a b; do
    other=$([ "${tenant}" = a ] && echo b || echo a)
    for provider in kubernetes spiffe; do
        kubectl -n "${APP_NS}" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: sa-${provider}-${tenant}
  namespace: ${APP_NS}
  annotations:
    credential.auth.fluxcd.io/provider: ${provider}
    credential.auth.fluxcd.io/type: jwt
    credential.auth.fluxcd.io/audiences: ${KUBECONFIG_AUDIENCE}
EOF
        kubectl -n "${APP_NS}" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: kc-${provider}-${tenant}
  namespace: ${APP_NS}
data:
  provider: generic
  address: "https://${REMOTE_CLUSTER}-control-plane:6443"
  serviceAccountName: sa-${provider}-${tenant}
  ca.crt: |
$(sed 's/^/    /' "${WORKDIR}/remote-ca.crt")
EOF
        kubectl --context "kind-${REMOTE_CLUSTER}" -n "tenant-${tenant}" apply -f - >/dev/null <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ${provider}-${tenant}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: deployer
subjects:
  - kind: User
    apiGroup: rbac.authorization.k8s.io
    name: $([ "${provider}" = kubernetes ] && echo "system:serviceaccount:${APP_NS}:sa-${provider}-${tenant}" || echo "spiffe://${SPIRE_TRUST_DOMAIN}/kubeconfig/kc-${provider}-${tenant}-own")
EOF
    done
done

##############################################################################
log "Deploying the source artifact"
##############################################################################
docker rm -f "${REGISTRY_NAME}" >/dev/null 2>&1 || true
docker run -d --restart=always --name "${REGISTRY_NAME}" --network kind \
    -p "127.0.0.1:${REGISTRY_PORT}:5000" "${REGISTRY_IMAGE}" >/dev/null
REGISTRY_IP="$(docker inspect -f '{{.NetworkSettings.Networks.kind.IPAddress}}' "${REGISTRY_NAME}")"
mkdir -p "${WORKDIR}/artifact"
cat > "${WORKDIR}/artifact/configmap.yaml" <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubeconfig-e2e
data:
  message: applied-through-remote-kubeconfig
EOF
cat > "${WORKDIR}/artifact/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - configmap.yaml
EOF
flux push artifact "oci://localhost:${REGISTRY_PORT}/kubeconfig-e2e:${REV}" \
    --path="${WORKDIR}/artifact" --source=kubeconfig-e2e --revision="${REV}" >/dev/null
kubectl -n "${APP_NS}" apply -f - >/dev/null <<EOF
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: kubeconfig-source
  namespace: ${APP_NS}
spec:
  interval: 1m
  insecure: true
  url: oci://${REGISTRY_IP}:5000/kubeconfig-e2e
  ref:
    tag: ${REV}
EOF
kubectl -n "${APP_NS}" wait ocirepository/kubeconfig-source --for=condition=Ready --timeout=2m

##############################################################################
log "Patching ${CONTROLLER} for the SPIFFE Broker"
##############################################################################
kubectl -n "${NS}" patch "deploy/${CONTROLLER}" --type=json -p "$(cat <<EOF
[
  {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--spiffe-broker-endpoint=${SPIRE_BROKER_ENDPOINT}"},
  {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--spiffe-broker-id=${SPIRE_BROKER_ID}"}
]
EOF
)" >/dev/null
kubectl -n "${NS}" patch "deploy/${CONTROLLER}" --type=strategic -p "$(cat <<EOF
spec:
  template:
    spec:
      containers:
        - name: manager
          env:
            - name: SPIFFE_ENDPOINT_SOCKET
              value: unix:///spiffe-workload-api/api.sock
          volumeMounts:
            - name: spiffe-workload-api
              mountPath: /spiffe-workload-api
              readOnly: true
      volumes:
        - name: spiffe-workload-api
          hostPath:
            path: /run/spire/agent-sockets
EOF
)" >/dev/null
kubectl -n "${NS}" rollout status "deploy/${CONTROLLER}" --timeout=2m

##############################################################################
log "Creating Kustomization resources for both providers and tenants"
##############################################################################
for tenant in a b; do
    other=$([ "${tenant}" = a ] && echo b || echo a)
    for provider in kubernetes spiffe; do
        for scope in own cross; do
            target_tenant="${tenant}"
            [ "${scope}" = cross ] && target_tenant="${other}"
            name="kc-${provider}-${tenant}-${scope}"
            kubectl -n "${APP_NS}" apply -f - >/dev/null <<EOF
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: ${name}
  namespace: ${APP_NS}
spec:
  interval: 1m
  retryInterval: 5s
  path: "./"
  prune: true
  targetNamespace: tenant-${target_tenant}
  sourceRef:
    kind: OCIRepository
    name: kubeconfig-source
  kubeConfig:
    configMapRef:
      name: kc-${provider}-${tenant}
EOF
        done
    done
done

wait_ready() {
    local name="$1"
    kubectl -n "${APP_NS}" wait "kustomization/${name}" --for=condition=Ready --timeout=3m
}

expect_denied() {
    local name="$1" status msg
    for _ in $(seq 1 60); do
        status="$(kubectl -n "${APP_NS}" get "kustomization/${name}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
        msg="$(kubectl -n "${APP_NS}" get "kustomization/${name}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null || true)"
        if [ "${status}" = "True" ]; then
            fail "${name} unexpectedly succeeded"
        fi
        if [ "${status}" = "False" ] && printf '%s' "${msg}" | grep -qiE 'forbidden|cannot|denied|unauthorized'; then
            info "${name} correctly denied"
            return 0
        fi
        sleep 2
    done
    fail "${name} did not fail with an authorization error in time (status=${status:-unknown}; message=${msg:-})"
}

for tenant in a b; do
    for provider in kubernetes spiffe; do
        name="kc-${provider}-${tenant}-own"
        wait_ready "${name}"
        kubectl --context "kind-${REMOTE_CLUSTER}" -n "tenant-${tenant}" get configmap/kubeconfig-e2e >/dev/null
        info "${name} applied to tenant-${tenant}"
        expect_denied "kc-${provider}-${tenant}-cross"
    done
done

log "SUCCESS: remote kubeconfig access is isolated per tenant for both providers"
