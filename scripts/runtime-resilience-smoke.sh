#!/usr/bin/env bash
set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

readonly SMOKE_PROJECT="game-rewards-runtime-smoke"
readonly SMOKE_DATABASE_URL="postgres://game_rewards:game_rewards_dev_password@127.0.0.1:5432/game_rewards?sslmode=disable"
readonly WAIT_TIMEOUT_SECONDS=45
readonly COMPOSE_WAIT_TIMEOUT_SECONDS=60
readonly CURL_TIMEOUT_SECONDS=5
# Require repeated failed claim iterations so the outage proves worker retry
# behavior rather than observing only one failure from a stale connection.
readonly REQUIRED_WORKER_OUTAGE_ERRORS=3
readonly RUN_ID="$(date +%s)-$$"
readonly PLAYER_ID="runtime-smoke-${RUN_ID}"
readonly REWARD_ID="runtime-smoke-reward"
readonly TMP_DIR="$(mktemp -d)"

smoke_project_owned=false

compose() {
  docker compose \
    --project-name "${SMOKE_PROJECT}" \
    --file "${REPO_ROOT}/compose.yaml" \
    "$@"
}

repository_compose_containers() {
  (
    unset COMPOSE_PROJECT_NAME COMPOSE_FILE
    docker compose --file "${REPO_ROOT}/compose.yaml" ps -q postgres api worker 2>/dev/null
  )
}

log() {
  printf '[runtime-resilience] %s\n' "$*"
}

fail() {
  printf '[runtime-resilience] ERROR: %s\n' "$*" >&2
  exit 1
}

diagnostics() {
  printf '\n[runtime-resilience] Compose state at failure:\n' >&2
  compose ps -a >&2 || true
  printf '\n[runtime-resilience] Recent service logs:\n' >&2
  compose logs --no-color --tail=200 postgres api worker >&2 || true
}

cleanup() {
  local status=$?
  local cleanup_status=0

  trap - EXIT INT TERM
  set +e

  if [[ "${smoke_project_owned}" == "true" ]]; then
    if (( status != 0 )); then
      diagnostics
    fi
    compose down --volumes --remove-orphans >/dev/null 2>&1
    cleanup_status=$?
    if (( cleanup_status != 0 )); then
      printf '[runtime-resilience] ERROR: failed to clean up smoke-test Compose resources\n' >&2
      if (( status == 0 )); then
        status=${cleanup_status}
      fi
    fi
  fi

  rm -rf "${TMP_DIR}"

  if (( status == 0 )); then
    log "runtime resilience smoke test passed"
  fi

  exit "${status}"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

http_status() {
  local output_file=$1
  shift
  local status

  if status=$(curl \
    --silent \
    --max-time "${CURL_TIMEOUT_SECONDS}" \
    --output "${output_file}" \
    --write-out '%{http_code}' \
    "$@"); then
    printf '%s' "${status}"
    return
  fi

  printf '000'
}

assert_http_status() {
  local name=$1
  local url=$2
  local expected=$3
  local output_file="${TMP_DIR}/${name}.json"
  local actual

  actual=$(http_status "${output_file}" "${url}")
  if [[ "${actual}" != "${expected}" ]]; then
    printf '[runtime-resilience] %s response body:\n' "${name}" >&2
    cat "${output_file}" >&2 2>/dev/null || true
    fail "${name}: expected HTTP ${expected}, got ${actual}"
  fi
}

wait_for_http_status() {
  local name=$1
  local url=$2
  local expected=$3
  local output_file="${TMP_DIR}/${name}.json"
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local actual="000"

  while (( SECONDS < deadline )); do
    actual=$(http_status "${output_file}" "${url}")
    if [[ "${actual}" == "${expected}" ]]; then
      return
    fi
    sleep 1
  done

  printf '[runtime-resilience] %s response body:\n' "${name}" >&2
  cat "${output_file}" >&2 2>/dev/null || true
  fail "${name}: expected HTTP ${expected}, last status was ${actual}"
}

assert_body_matches() {
  local name=$1
  local pattern=$2
  local output_file="${TMP_DIR}/${name}.json"

  grep -Eq "${pattern}" "${output_file}" || {
    printf '[runtime-resilience] %s response body:\n' "${name}" >&2
    cat "${output_file}" >&2 2>/dev/null || true
    fail "${name}: response body did not match expected JSON field"
  }
}

worker_claim_operation_errors() {
  local metrics_file="${TMP_DIR}/worker-metrics.prom"

  if ! curl \
    --silent \
    --fail \
    --max-time "${CURL_TIMEOUT_SECONDS}" \
    --output "${metrics_file}" \
    http://127.0.0.1:8081/metrics; then
    return 1
  fi

  awk '
    $1 == "game_rewards_outbox_operation_errors_total{operation=\"claim\"}" {
      print $2
      found = 1
    }
    END {
      if (!found) {
        print 0
      }
    }
  ' "${metrics_file}"
}

wait_for_worker_claim_operation_errors() {
  local baseline=$1
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local current=""

  while (( SECONDS < deadline )); do
    if current=$(worker_claim_operation_errors); then
      if awk \
        -v current="${current}" \
        -v baseline="${baseline}" \
        -v required="${REQUIRED_WORKER_OUTAGE_ERRORS}" \
        'BEGIN { exit !(current >= baseline + required) }'; then
        return
      fi
    fi
    sleep 1
  done

  fail "worker did not record ${REQUIRED_WORKER_OUTAGE_ERRORS} claim store errors during PostgreSQL outage; baseline=${baseline}, last=${current:-unavailable}"
}

post_claim() {
  local phase=$1
  local expected_status=$2
  local output_file="${TMP_DIR}/claim-${phase}.json"
  local payload
  local actual

  payload=$(printf \
    '{"player_id":"%s","campaign_id":"%s","reward_id":"%s"}' \
    "${PLAYER_ID}" \
    "${phase}" \
    "${REWARD_ID}")

  actual=$(http_status \
    "${output_file}" \
    --request POST \
    --header 'Content-Type: application/json' \
    --header "Idempotency-Key: runtime-smoke-${phase}-${RUN_ID}" \
    --data "${payload}" \
    http://127.0.0.1:8080/v1/reward-claims)

  if [[ "${actual}" != "${expected_status}" ]]; then
    printf '[runtime-resilience] claim %s response body:\n' "${phase}" >&2
    cat "${output_file}" >&2 2>/dev/null || true
    fail "claim ${phase}: expected HTTP ${expected_status}, got ${actual}"
  fi

  if [[ "${expected_status}" == "503" ]]; then
    grep -Eq '"code"[[:space:]]*:[[:space:]]*"service_unavailable"' "${output_file}" || {
      printf '[runtime-resilience] claim %s response body:\n' "${phase}" >&2
      cat "${output_file}" >&2 2>/dev/null || true
      fail "claim ${phase}: expected service_unavailable error"
    }
  fi
}

outbox_status() {
  local phase=$1
  local sql

  sql="SELECT o.status
FROM outbox_events AS o
JOIN reward_claims AS r ON r.id = o.aggregate_id
WHERE r.player_id = '${PLAYER_ID}'
  AND r.campaign_id = '${phase}'
  AND r.reward_id = '${REWARD_ID}'
  AND o.aggregate_type = 'reward_claim'
  AND o.event_type = 'RewardClaimed'
LIMIT 1;"

  compose exec -T postgres \
    psql -U game_rewards -d game_rewards -Atqc "${sql}" 2>/dev/null || true
}

wait_for_outbox_published() {
  local phase=$1
  local deadline=$((SECONDS + WAIT_TIMEOUT_SECONDS))
  local status=""

  while (( SECONDS < deadline )); do
    status=$(outbox_status "${phase}")
    if [[ "${status}" == "published" ]]; then
      return
    fi
    sleep 1
  done

  fail "outbox event for ${phase} claim was not published; last status: ${status:-missing}"
}

container_id() {
  local service=$1
  compose ps -q "${service}"
}

container_started_at() {
  local id=$1
  docker inspect --format '{{.State.StartedAt}}' "${id}"
}

assert_container_unchanged() {
  local service=$1
  local expected_id=$2
  local expected_started_at=$3
  local actual_id
  local actual_started_at
  local running

  actual_id=$(container_id "${service}")
  [[ -n "${actual_id}" ]] || fail "${service} container is not running"

  actual_started_at=$(container_started_at "${actual_id}")
  running=$(docker inspect --format '{{.State.Running}}' "${actual_id}")

  [[ "${running}" == "true" ]] || fail "${service} container is not running"
  [[ "${actual_id}" == "${expected_id}" ]] || fail "${service} container was recreated"
  [[ "${actual_started_at}" == "${expected_started_at}" ]] || fail "${service} container restarted"
}

require_command docker
require_command curl
require_command make
require_command awk

docker info >/dev/null 2>&1 || fail "Docker daemon is not available"
docker compose version >/dev/null 2>&1 || fail "Docker Compose is not available"

if [[ -n "$(compose ps -q 2>/dev/null || true)" ]]; then
  fail "smoke-test Compose project ${SMOKE_PROJECT} is already running"
fi
compose down --volumes --remove-orphans >/dev/null 2>&1 || true

if [[ -n "$(repository_compose_containers || true)" ]]; then
  fail "the repository Compose stack is already running; stop it before the runtime resilience test"
fi

log "building runtime image"
make docker-build

log "starting isolated PostgreSQL"
smoke_project_owned=true
compose up -d --wait --wait-timeout "${COMPOSE_WAIT_TIMEOUT_SECONDS}" postgres

log "applying migrations"
make migrate-up DATABASE_URL="${SMOKE_DATABASE_URL}"

log "starting API and worker from the prebuilt image"
compose up -d --no-build --wait --wait-timeout "${COMPOSE_WAIT_TIMEOUT_SECONDS}" api worker

wait_for_http_status api-live-baseline http://127.0.0.1:8080/livez 200
wait_for_http_status api-ready-baseline http://127.0.0.1:8080/readyz 200
wait_for_http_status worker-live-baseline http://127.0.0.1:8081/livez 200
wait_for_http_status worker-ready-baseline http://127.0.0.1:8081/readyz 200

post_claim baseline 201
wait_for_outbox_published baseline
worker_claim_errors_before=$(worker_claim_operation_errors) || \
  fail "could not read worker metrics before PostgreSQL outage"

api_id_before=$(container_id api)
worker_id_before=$(container_id worker)
[[ -n "${api_id_before}" ]] || fail "API container id is missing"
[[ -n "${worker_id_before}" ]] || fail "worker container id is missing"
api_started_before=$(container_started_at "${api_id_before}")
worker_started_before=$(container_started_at "${worker_id_before}")

log "removing PostgreSQL container while preserving its named volume"
compose rm -s -f postgres

log "waiting for repeated worker store failures during PostgreSQL outage"
wait_for_worker_claim_operation_errors "${worker_claim_errors_before}"
wait_for_http_status api-ready-outage http://127.0.0.1:8080/readyz 503
wait_for_http_status worker-ready-outage http://127.0.0.1:8081/readyz 503
assert_http_status api-live-outage http://127.0.0.1:8080/livez 200
assert_http_status worker-live-outage http://127.0.0.1:8081/livez 200
assert_body_matches api-ready-outage '"postgres"[[:space:]]*:[[:space:]]*"error"'
assert_body_matches worker-ready-outage '"postgres"[[:space:]]*:[[:space:]]*"error"'
assert_body_matches worker-ready-outage '"worker"[[:space:]]*:[[:space:]]*"ok"'
post_claim outage 503

assert_container_unchanged api "${api_id_before}" "${api_started_before}"
assert_container_unchanged worker "${worker_id_before}" "${worker_started_before}"

log "recreating PostgreSQL from the preserved named volume"
compose up -d --no-build --wait --wait-timeout "${COMPOSE_WAIT_TIMEOUT_SECONDS}" postgres

wait_for_http_status api-ready-recovery http://127.0.0.1:8080/readyz 200
wait_for_http_status worker-ready-recovery http://127.0.0.1:8081/readyz 200
assert_http_status api-live-recovery http://127.0.0.1:8080/livez 200
assert_http_status worker-live-recovery http://127.0.0.1:8081/livez 200

assert_container_unchanged api "${api_id_before}" "${api_started_before}"
assert_container_unchanged worker "${worker_id_before}" "${worker_started_before}"

post_claim recovery 201
wait_for_outbox_published recovery
