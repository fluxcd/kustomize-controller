#!/usr/bin/env bash
# End-to-end test for SOPS decryption with OpenBao/Vault using the controller's
# Kubernetes ServiceAccount identity (the --sops-vault-configmap feature), across
# TWO clusters and TWO OpenBao instances exercising BOTH supported auth methods.
#
# Topology (all containers share kind's docker network so the SAME URL works
# from the host and from inside pods):
#
#   registry        : registry:2, holds the OCI artifact with both secrets
#   openbao-a       : transit + Kubernetes auth method (TokenReview)
#   openbao-b       : transit + JWT auth method (offline, sa.pub validation)
#   sops-e2e-1/-2   : two kind clusters, each running source-controller and
#                     kustomize-controller (controller-level workload identity)
#
#   secret-a is encrypted by openbao-a, secret-b by openbao-b. Each cluster
#   reconciles a Kustomization that decrypts BOTH, so every controller must
#   authenticate to BOTH OpenBaos (one per auth method) using only its own
#   ServiceAccount token, audience-scoped to each OpenBao address.
#
# Run locally with: IMG=test/kustomize-controller:latest hack/ci/sops-openbao-e2e.sh
# (build/load the image yourself first, or let the e2e workflow do it).
set -euo pipefail

IMG="${IMG:-test/kustomize-controller:latest}"
OPENBAO_IMAGE="${OPENBAO_IMAGE:-openbao/openbao:2.5.4}"
REGISTRY_IMAGE="${REGISTRY_IMAGE:-registry:2}"
KIND_NET=kind
NS=kustomize-system            # controller namespace == RUNTIME_NAMESPACE
APP_NS=sops-test               # where the decrypted Secrets are applied
# The first cluster is the one the CI setup action already created ("kind"); the
# rest are created (and torn down) by this script. Reusable across local runs.
CLUSTERS=(kind sops-e2e-2)
CREATED_CLUSTERS=()
CONFIGMAP=sops-vault-config
REV=v1
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKDIR="$(mktemp -d)"

log()  { echo -e "\n\033[1;34m==> $*\033[0m"; }
info() { echo "    $*"; }
fail() { echo -e "\033[1;31mFAIL: $*\033[0m" >&2; exit 1; }

baoA() { docker exec -i -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=root openbao-a bao "$@"; }
baoB() { docker exec -i -e BAO_ADDR=http://127.0.0.1:8200 -e BAO_TOKEN=root openbao-b bao "$@"; }
ip_of() { docker inspect -f "{{.NetworkSettings.Networks.${KIND_NET}.IPAddress}}" "$1"; }

cleanup() {
  local code=$?
  if [ "$code" -ne 0 ]; then
    log "FAILURE (exit ${code}) — dumping diagnostics"
    for c in "${CLUSTERS[@]}"; do
      echo "##### cluster ${c} #####"
      kubectl --context "kind-${c}" -n "${NS}" logs deploy/kustomize-controller --tail=80 2>/dev/null || true
      kubectl --context "kind-${c}" -n "${APP_NS}" get ocirepositories,kustomizations -o wide 2>/dev/null || true
      kubectl --context "kind-${c}" -n "${APP_NS}" get kustomization sops-secrets -o yaml 2>/dev/null | grep -A30 'status:' || true
    done
    docker logs --tail=40 openbao-a 2>/dev/null || true
    docker logs --tail=40 openbao-b 2>/dev/null || true
  fi
  if [ -n "${KEEP:-}" ]; then
    log "KEEP set — leaving clusters, containers and ${WORKDIR} for inspection"
    exit "${code}"
  fi
  log "Cleaning up"
  # Only delete clusters this script created; leave any pre-existing one intact.
  for c in "${CREATED_CLUSTERS[@]:-}"; do
    [ -n "${c}" ] && kind delete cluster --name "${c}" >/dev/null 2>&1 || true
  done
  docker rm -f registry openbao-a openbao-b >/dev/null 2>&1 || true
  rm -rf "${WORKDIR}"
  exit "${code}"
}
trap cleanup EXIT

require() { command -v "$1" >/dev/null 2>&1 || fail "required tool '$1' not found in PATH"; }
for bin in docker kind kubectl kustomize sops flux; do require "$bin"; done

##############################################################################
log "Ensuring ${#CLUSTERS[@]} kind clusters exist"
##############################################################################
existing="$(kind get clusters 2>/dev/null || true)"
for c in "${CLUSTERS[@]}"; do
  if echo "${existing}" | grep -qx "${c}"; then
    info "reusing existing cluster '${c}'"
  else
    kind create cluster --name "${c}" --wait 3m
    CREATED_CLUSTERS+=("${c}")
  fi
done
# kind owns the shared docker network; attach the supporting containers to it.

##############################################################################
log "Starting registry + two OpenBao dev servers on the '${KIND_NET}' network"
##############################################################################
docker run -d --restart=always --name registry --network "${KIND_NET}" -p 5000:5000 "${REGISTRY_IMAGE}" >/dev/null
for name in openbao-a openbao-b; do
  docker run -d --name "${name}" --network "${KIND_NET}" --cap-add=IPC_LOCK \
    -e BAO_DEV_ROOT_TOKEN_ID=root -e BAO_DEV_LISTEN_ADDRESS=0.0.0.0:8200 \
    "${OPENBAO_IMAGE}" server -dev >/dev/null
done

# Wait for OpenBao to be ready.
for fn in baoA baoB; do
  for _ in $(seq 1 30); do
    if "${fn}" status >/dev/null 2>&1; then break; fi
    sleep 1
  done
  "${fn}" status >/dev/null 2>&1 || fail "OpenBao (${fn}) did not become ready"
done

REG_IP="$(ip_of registry)"
BAO_A_IP="$(ip_of openbao-a)"; BAO_A_URL="http://${BAO_A_IP}:8200"
BAO_B_IP="$(ip_of openbao-b)"; BAO_B_URL="http://${BAO_B_IP}:8200"
info "registry  = ${REG_IP}:5000"
info "openbao-a = ${BAO_A_URL} (kubernetes auth)"
info "openbao-b = ${BAO_B_URL} (jwt auth)"

##############################################################################
log "Configuring transit engines and decrypt policies"
##############################################################################
for fn in baoA baoB; do
  "${fn}" secrets enable transit >/dev/null 2>&1 || true
  "${fn}" write -f transit/keys/sops >/dev/null
  "${fn}" policy write sops - >/dev/null <<'EOF'
path "transit/decrypt/sops" { capabilities = ["update"] }
EOF
done

##############################################################################
log "Encrypting two Secrets (one per OpenBao) with SOPS transit"
##############################################################################
mkdir -p "${WORKDIR}/manifests"

cat > "${WORKDIR}/secret-a.yaml" <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: sops-secret-a
type: Opaque
stringData:
  message: "decrypted-via-openbao-a-kubernetes-auth"
EOF
cat > "${WORKDIR}/secret-b.yaml" <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: sops-secret-b
type: Opaque
stringData:
  message: "decrypted-via-openbao-b-jwt-auth"
EOF

VAULT_ADDR="${BAO_A_URL}" VAULT_TOKEN=root sops \
  --hc-vault-transit "${BAO_A_URL}/v1/transit/keys/sops" \
  --encrypted-regex '^(data|stringData)$' \
  -e "${WORKDIR}/secret-a.yaml" > "${WORKDIR}/manifests/secret-a.enc.yaml"
VAULT_ADDR="${BAO_B_URL}" VAULT_TOKEN=root sops \
  --hc-vault-transit "${BAO_B_URL}/v1/transit/keys/sops" \
  --encrypted-regex '^(data|stringData)$' \
  -e "${WORKDIR}/secret-b.yaml" > "${WORKDIR}/manifests/secret-b.enc.yaml"

cat > "${WORKDIR}/manifests/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - secret-a.enc.yaml
  - secret-b.enc.yaml
EOF

info "embedded vault addresses:"
grep 'vault_address' "${WORKDIR}/manifests/"*.enc.yaml | sed 's/^/      /'

##############################################################################
log "Pushing the encrypted manifests as an OCI artifact"
##############################################################################
# Push over HTTP via the published localhost port (go-containerregistry treats
# localhost as insecure); pods pull the same content via the kind-network IP.
flux push artifact "oci://localhost:5000/sops-secrets:${REV}" \
  --path="${WORKDIR}/manifests" \
  --source=sops-openbao-e2e \
  --revision="${REV}"

##############################################################################
# Per-cluster: deploy controllers, onboard to both OpenBaos, apply the source.
#
# Cluster 1 exercises CONTROLLER-LEVEL workload identity (the controller's own
# 'default' ServiceAccount, no feature gate). Cluster 2 exercises OBJECT-LEVEL
# workload identity (a per-Kustomization 'serviceAccountName' with the
# ObjectLevelWorkloadIdentity feature gate). Either way the Vault role name is
# derived from the authenticating ServiceAccount as "{namespace}_{name}".
##############################################################################
DECRYPTOR_SA=sops-decryptor   # object-level ServiceAccount (in APP_NS)

onboard_cluster() {
  local idx="$1" cluster="$2"
  local ctx="kind-${cluster}" node="${cluster}-control-plane"
  local k8s_host="https://$(ip_of "${node}"):6443"

  # Identity model for this cluster: cluster 1 = controller-level (the
  # controller's own ServiceAccount), cluster 2 = object-level (a per-
  # Kustomization ServiceAccount + the ObjectLevelWorkloadIdentity feature gate).
  local level sa_ns sa_name object_level
  if [ "${idx}" -eq 1 ]; then
    level="controller-level"; sa_ns="${NS}";     object_level=false
  else
    level="object-level";     sa_ns="${APP_NS}"; sa_name="${DECRYPTOR_SA}"; object_level=true
  fi

  log "[cluster ${idx}] ${cluster}: loading image and deploying controllers"
  kind load docker-image "${IMG}" --name "${cluster}"
  kubectl config use-context "${ctx}" >/dev/null
  make -C "${REPO_ROOT}" dev-deploy IMG="${IMG}" >/dev/null

  # For controller-level, derive the controller's actual ServiceAccount from the
  # Deployment (config/ runs as the namespace 'default' SA, but don't assume it).
  if [ "${object_level}" = false ]; then
    sa_name="$(kubectl --context "${ctx}" -n "${NS}" get deploy kustomize-controller \
      -o jsonpath='{.spec.template.spec.serviceAccountName}' 2>/dev/null)"
    [ -n "${sa_name}" ] || sa_name=default
  fi
  local role="${sa_ns}_${sa_name}"
  info "[cluster ${idx}] identity: ${level} -> ${sa_ns}/${sa_name} (Vault role '${role}')"

  # The decrypted Secrets and (for object-level) the decryption ServiceAccount
  # live in the application namespace.
  kubectl --context "${ctx}" create namespace "${APP_NS}" \
    --dry-run=client -o yaml | kubectl --context "${ctx}" apply -f - >/dev/null
  if [ "${object_level}" = true ]; then
    kubectl --context "${ctx}" -n "${APP_NS}" create serviceaccount "${sa_name}" \
      --dry-run=client -o yaml | kubectl --context "${ctx}" apply -f - >/dev/null
  fi

  log "[cluster ${idx}] Creating the Vault ConfigMap (address -> login path allowlist)"
  cat > "${WORKDIR}/vault-config-${idx}.yaml" <<EOF
instances:
  - address: ${BAO_A_URL}
    loginPath: auth/kubernetes-c${idx}/login
  - address: ${BAO_B_URL}
    loginPath: auth/jwt-c${idx}/login
EOF
  kubectl --context "${ctx}" -n "${NS}" create configmap "${CONFIGMAP}" \
    --from-file=config.yaml="${WORKDIR}/vault-config-${idx}.yaml" \
    --dry-run=client -o yaml | kubectl --context "${ctx}" apply -f - >/dev/null

  log "[cluster ${idx}] Enabling the --sops-vault-configmap flag (+ feature gate for object-level)"
  local patch="[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/args/-\",\"value\":\"--sops-vault-configmap=${CONFIGMAP}\"}"
  if [ "${object_level}" = true ]; then
    patch+=",{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/args/-\",\"value\":\"--feature-gates=ObjectLevelWorkloadIdentity=true\"}"
  fi
  patch+="]"
  kubectl --context "${ctx}" -n "${NS}" patch deploy kustomize-controller --type=json -p "${patch}" >/dev/null
  kubectl --context "${ctx}" -n "${NS}" rollout status deploy/kustomize-controller --timeout=2m
  kubectl --context "${ctx}" -n "${NS}" rollout status deploy/source-controller --timeout=2m

  log "[cluster ${idx}] Creating reviewer ServiceAccount (for OpenBao-A Kubernetes auth)"
  kubectl --context "${ctx}" apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ServiceAccount
metadata: { name: openbao-reviewer, namespace: ${NS} }
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: { name: openbao-reviewer-auth-delegator }
roleRef: { apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: system:auth-delegator }
subjects:
- { kind: ServiceAccount, name: openbao-reviewer, namespace: ${NS} }
---
apiVersion: v1
kind: Secret
metadata:
  name: openbao-reviewer-token
  namespace: ${NS}
  annotations: { kubernetes.io/service-account.name: openbao-reviewer }
type: kubernetes.io/service-account-token
EOF

  for _ in $(seq 1 20); do
    if kubectl --context "${ctx}" -n "${NS}" get secret openbao-reviewer-token \
         -o jsonpath='{.data.token}' 2>/dev/null | grep -q .; then break; fi
    sleep 1
  done
  kubectl --context "${ctx}" -n "${NS}" get secret openbao-reviewer-token \
    -o jsonpath='{.data.token}'  | base64 -d > "${WORKDIR}/reviewer-${idx}.jwt"
  kubectl --context "${ctx}" -n "${NS}" get secret openbao-reviewer-token \
    -o jsonpath='{.data.ca\.crt}' | base64 -d > "${WORKDIR}/ca-${idx}.crt"
  # The SA token signing public key, used by OpenBao-B's JWT auth to validate
  # tokens offline (no callback to the API server).
  docker exec "${node}" cat /etc/kubernetes/pki/sa.pub > "${WORKDIR}/sa-${idx}.pub"

  log "[cluster ${idx}] Onboarding to openbao-a (Kubernetes auth mount kubernetes-c${idx})"
  docker cp "${WORKDIR}/reviewer-${idx}.jwt" "openbao-a:/tmp/reviewer-${idx}.jwt" >/dev/null
  docker cp "${WORKDIR}/ca-${idx}.crt"       "openbao-a:/tmp/ca-${idx}.crt"       >/dev/null
  baoA auth enable -path="kubernetes-c${idx}" kubernetes >/dev/null 2>&1 || true
  baoA write "auth/kubernetes-c${idx}/config" \
    kubernetes_host="${k8s_host}" \
    kubernetes_ca_cert=@/tmp/ca-${idx}.crt \
    token_reviewer_jwt=@/tmp/reviewer-${idx}.jwt >/dev/null
  baoA write "auth/kubernetes-c${idx}/role/${role}" \
    bound_service_account_names="${sa_name}" \
    bound_service_account_namespaces="${sa_ns}" \
    token_policies=sops \
    audience="${BAO_A_URL}" \
    ttl=20m >/dev/null

  log "[cluster ${idx}] Onboarding to openbao-b (JWT auth mount jwt-c${idx})"
  docker cp "${WORKDIR}/sa-${idx}.pub" "openbao-b:/tmp/sa-${idx}.pub" >/dev/null
  baoB auth enable -path="jwt-c${idx}" jwt >/dev/null 2>&1 || true
  baoB write "auth/jwt-c${idx}/config" \
    jwt_validation_pubkeys=@/tmp/sa-${idx}.pub >/dev/null
  baoB write "auth/jwt-c${idx}/role/${role}" \
    role_type=jwt \
    user_claim=sub \
    bound_subject="system:serviceaccount:${sa_ns}:${sa_name}" \
    bound_audiences="${BAO_B_URL}" \
    token_policies=sops \
    ttl=20m >/dev/null

  log "[cluster ${idx}] Applying the OCIRepository + Kustomization source (${level})"
  local decryption="  decryption:
    provider: sops"
  if [ "${object_level}" = true ]; then
    decryption+="
    serviceAccountName: ${sa_name}"
  fi
  kubectl --context "${ctx}" apply -f - >/dev/null <<EOF
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: sops-secrets
  namespace: ${APP_NS}
spec:
  interval: 1m
  insecure: true
  url: oci://${REG_IP}:5000/sops-secrets
  ref:
    tag: ${REV}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: sops-secrets
  namespace: ${APP_NS}
spec:
  interval: 1m
  retryInterval: 10s
  path: "./"
  prune: true
  wait: true
  timeout: 2m
  targetNamespace: ${APP_NS}
  sourceRef:
    kind: OCIRepository
    name: sops-secrets
${decryption}
EOF
}

for i in "${!CLUSTERS[@]}"; do
  onboard_cluster "$((i + 1))" "${CLUSTERS[$i]}"
done

##############################################################################
log "Verifying that BOTH clusters decrypted BOTH secrets"
##############################################################################
expect_secret() {
  local ctx="$1" name="$2" want="$3"
  local got
  got="$(kubectl --context "${ctx}" -n "${APP_NS}" get secret "${name}" \
    -o jsonpath='{.data.message}' 2>/dev/null | base64 -d || true)"
  [ "${got}" = "${want}" ] || fail "${ctx}: secret '${name}' message='${got}', want='${want}'"
  info "${ctx}: ${name} OK"
}

for i in "${!CLUSTERS[@]}"; do
  ctx="kind-${CLUSTERS[$i]}"
  kubectl --context "${ctx}" -n "${APP_NS}" wait kustomization/sops-secrets \
    --for=condition=ready --timeout=3m
  expect_secret "${ctx}" sops-secret-a "decrypted-via-openbao-a-kubernetes-auth"
  expect_secret "${ctx}" sops-secret-b "decrypted-via-openbao-b-jwt-auth"
done

##############################################################################
log "Negative tests: verifying permission/denial errors are enforced"
##############################################################################
NEG_NS=sops-negative
NEG_CTX1="kind-${CLUSTERS[0]}"   # controller-level, ObjectLevelWorkloadIdentity OFF
NEG_CTX2="kind-${CLUSTERS[1]}"   # object-level, feature gate ON

# expect_failure waits for a Kustomization to settle NOT-ready with a Ready
# condition message containing the expected substring (and never become ready).
expect_failure() {
  local ctx="$1" name="$2" want="$3" status msg
  for _ in $(seq 1 40); do
    status="$(kubectl --context "${ctx}" -n "${NEG_NS}" get kustomization "${name}" \
      -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
    msg="$(kubectl --context "${ctx}" -n "${NEG_NS}" get kustomization "${name}" \
      -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}' 2>/dev/null || true)"
    if [ "${status}" = "True" ]; then
      fail "${ctx}: '${name}' unexpectedly succeeded (expected denial containing '${want}')"
    fi
    if [ "${status}" = "False" ] && printf '%s' "${msg}" | grep -qiF "${want}"; then
      info "${ctx}: ${name} correctly denied (${want})"
      return 0
    fi
    sleep 3
  done
  fail "${ctx}: '${name}' did not fail with '${want}' in time (status=${status}; msg=${msg})"
}

# neg_push builds a single-Secret OCI artifact from an already-encrypted file.
neg_push() {
  local tag="$1" encfile="$2"
  local dir="${WORKDIR}/art-${tag}"
  mkdir -p "${dir}"
  cp "${encfile}" "${dir}/secret.enc.yaml"
  cat > "${dir}/kustomization.yaml" <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - secret.enc.yaml
EOF
  flux push artifact "oci://localhost:5000/${tag}:${REV}" \
    --path="${dir}" --source=sops-openbao-e2e-negative --revision="${REV}" >/dev/null
}

# neg_apply creates a failing OCIRepository + Kustomization in the negative
# namespace, optionally with an object-level decryption ServiceAccount.
neg_apply() {
  local ctx="$1" name="$2" tag="$3" sa="${4:-}"
  local dec="  decryption:
    provider: sops"
  [ -n "${sa}" ] && dec+="
    serviceAccountName: ${sa}"
  kubectl --context "${ctx}" apply -f - >/dev/null <<EOF
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: ${name}
  namespace: ${NEG_NS}
spec:
  interval: 1m
  insecure: true
  url: oci://${REG_IP}:5000/${tag}
  ref:
    tag: ${REV}
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: ${name}
  namespace: ${NEG_NS}
spec:
  interval: 1m
  retryInterval: 5s
  timeout: 1m
  path: "./"
  prune: true
  targetNamespace: ${NEG_NS}
  sourceRef:
    kind: OCIRepository
    name: ${name}
${dec}
EOF
}

for ctx in "${NEG_CTX1}" "${NEG_CTX2}"; do
  kubectl --context "${ctx}" create namespace "${NEG_NS}" \
    --dry-run=client -o yaml | kubectl --context "${ctx}" apply -f - >/dev/null
done

# Build the encrypted Secrets reused by the negative cases.
cat > "${WORKDIR}/neg-secret.yaml" <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: neg-secret
type: Opaque
stringData:
  message: should-not-decrypt
EOF
# (a) encrypted with OpenBao-A's allowed 'sops' transit key
VAULT_ADDR="${BAO_A_URL}" VAULT_TOKEN=root sops \
  --hc-vault-transit "${BAO_A_URL}/v1/transit/keys/sops" \
  --encrypted-regex '^(data|stringData)$' \
  -e "${WORKDIR}/neg-secret.yaml" > "${WORKDIR}/neg-a.enc.yaml"
# (b) encrypted with a NEW 'restricted' key the 'sops' policy can NOT decrypt
baoA write -f transit/keys/restricted >/dev/null
VAULT_ADDR="${BAO_A_URL}" VAULT_TOKEN=root sops \
  --hc-vault-transit "${BAO_A_URL}/v1/transit/keys/restricted" \
  --encrypted-regex '^(data|stringData)$' \
  -e "${WORKDIR}/neg-secret.yaml" > "${WORKDIR}/neg-restricted.enc.yaml"
# (c) the 'sops'-key secret rewritten to an address absent from every allowlist
sed 's#vault_address: .*#vault_address: http://10.255.255.254:8200#' \
  "${WORKDIR}/neg-a.enc.yaml" > "${WORKDIR}/neg-allowlist.enc.yaml"

neg_push neg-a          "${WORKDIR}/neg-a.enc.yaml"
neg_push neg-restricted "${WORKDIR}/neg-restricted.enc.yaml"
neg_push neg-allowlist  "${WORKDIR}/neg-allowlist.enc.yaml"

# N1 — login succeeds but the token policy does not grant transit decrypt for the key.
log "[neg] N1 (${NEG_CTX1}): transit decrypt not permitted by policy -> permission denied"
neg_apply "${NEG_CTX1}" n1-decrypt-denied neg-restricted
expect_failure "${NEG_CTX1}" n1-decrypt-denied "permission denied"

# N2 — object-level requested but the ObjectLevelWorkloadIdentity feature gate is off.
log "[neg] N2 (${NEG_CTX1}): serviceAccountName set but feature gate disabled"
neg_apply "${NEG_CTX1}" n2-no-feature-gate neg-a some-decryptor-sa
expect_failure "${NEG_CTX1}" n2-no-feature-gate "ObjectLevelWorkloadIdentity"

# N3 — object-level login denied: no Vault role bound to the decryption ServiceAccount.
log "[neg] N3 (${NEG_CTX2}): no Vault role for the decryption ServiceAccount"
kubectl --context "${NEG_CTX2}" -n "${NEG_NS}" create serviceaccount unbound \
  --dry-run=client -o yaml | kubectl --context "${NEG_CTX2}" apply -f - >/dev/null
neg_apply "${NEG_CTX2}" n3-no-role neg-a unbound
expect_failure "${NEG_CTX2}" n3-no-role "failed to authenticate with Vault"

# N4 — object-level login denied: Vault role bound to the wrong token audience.
log "[neg] N4 (${NEG_CTX2}): Vault role audience mismatch"
kubectl --context "${NEG_CTX2}" -n "${NEG_NS}" create serviceaccount wrongaud \
  --dry-run=client -o yaml | kubectl --context "${NEG_CTX2}" apply -f - >/dev/null
baoA write "auth/kubernetes-c2/role/${NEG_NS}_wrongaud" \
  bound_service_account_names=wrongaud \
  bound_service_account_namespaces="${NEG_NS}" \
  token_policies=sops \
  audience="http://wrong.example:8200" \
  ttl=20m >/dev/null
neg_apply "${NEG_CTX2}" n4-wrong-audience neg-a wrongaud
expect_failure "${NEG_CTX2}" n4-wrong-audience "audience"

# N5 — the encrypted data key references a Vault address not in the ConfigMap allowlist.
log "[neg] N5 (${NEG_CTX1}): Vault address not in the ConfigMap allowlist"
neg_apply "${NEG_CTX1}" n5-not-allowlisted neg-allowlist
expect_failure "${NEG_CTX1}" n5-not-allowlisted "no Vault login path configured"

# N6 — authenticated but unauthorized: a valid Vault role (correct ServiceAccount
# binding and audience, so login SUCCEEDS) whose token policy does NOT grant
# transit decrypt. This "valid role, no permission" path is exercised on BOTH
# clusters: N1 covers the controller-level cluster (the controller's own role
# authenticates but its policy can't decrypt the 'restricted' key), and N6 below
# covers the object-level cluster with a dedicated powerless role.
log "[neg] N6 (${NEG_CTX2}): valid role, login succeeds, but no transit decrypt permission"
baoA policy write nodecrypt - >/dev/null <<'EOF'
path "sys/health" { capabilities = ["read"] }
EOF
kubectl --context "${NEG_CTX2}" -n "${NEG_NS}" create serviceaccount nodecrypt \
  --dry-run=client -o yaml | kubectl --context "${NEG_CTX2}" apply -f - >/dev/null
baoA write "auth/kubernetes-c2/role/${NEG_NS}_nodecrypt" \
  bound_service_account_names=nodecrypt \
  bound_service_account_namespaces="${NEG_NS}" \
  token_policies=nodecrypt \
  audience="${BAO_A_URL}" \
  ttl=20m >/dev/null
neg_apply "${NEG_CTX2}" n6-no-permission neg-a nodecrypt
expect_failure "${NEG_CTX2}" n6-no-permission "permission denied"

log "SUCCESS: both clusters decrypted both secrets, and all negative permission cases were denied"
