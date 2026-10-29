#!/usr/bin/env bash
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
JAR="$(mktemp)"
trap 'curl -fsS -b "$JAR" -H "Content-Type: application/json" -X PUT -d "{\"stage\":\"\"}" "$BASE/api/admin/failure" >/dev/null 2>&1 || true; rm -f "$JAR"' EXIT

pass() { printf 'PASS  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

api() {
  local m="$1" p="$2" b="${3:-}"
  if [ -n "$b" ]; then
    curl -fsS -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' -X "$m" -d "$b" "$BASE$p"
  else
    curl -fsS -b "$JAR" -c "$JAR" -X "$m" "$BASE$p"
  fi
}

login() { api POST /login "{\"user_id\":\"$1\"}" >/dev/null; }
stock() { api GET /api/products | jq -r --arg s "$1" '.[] | select(.sku==$s) | .stock'; }

wait_state() {
  local id="$1" want="$2" t="${3:-30}" state=""
  for _ in $(seq 1 "$t"); do
    state="$(api GET "/api/orders/$id" | jq -r .state)"
    [ "$state" = "$want" ] && return 0
    sleep 1
  done
  fail "order $id: expected $want, last state $state"
}

place() { api POST /api/orders "{\"items\":[{\"sku\":\"$1\",\"quantity\":$2}]}" | jq -r .id; }

echo "== waiting for gateway"
for _ in $(seq 1 30); do curl -fsS "$BASE/api/users" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS "$BASE/api/users" >/dev/null || fail "gateway not reachable at $BASE"

echo "== 1. happy path (alice, autopay)"
login u-alice
api GET /api/me | jq -e '.cards[0].autopay_consent == true' >/dev/null || fail "alice should have autopay consent"
api PUT /api/admin/failure '{"stage":""}' >/dev/null
id="$(place MS-01 1)"
wait_state "$id" COMPLETED
api GET "/api/orders/$id" | jq -e '[.history[].to] == ["INVENTORY_RESERVED","INVOICED","PAID","COMPLETED"]' >/dev/null \
  || fail "unexpected saga history for $id"
pass "order $id reached COMPLETED via INVENTORY_RESERVED > INVOICED > PAID > COMPLETED"

echo "== 2. no autopay consent (bob)"
login u-bob
id="$(place MS-01 1)"
wait_state "$id" INVOICED
sleep 2
[ "$(api GET "/api/orders/$id" | jq -r .state)" = "INVOICED" ] || fail "bob's order must wait for manual payment"
pass "order $id waits at INVOICED (payment.pending_manual)"

echo "== 3. failure switch (alice, sale fails, authorization voided)"
login u-alice
before="$(stock KB-01)"
api PUT /api/admin/failure '{"stage":"sale"}' >/dev/null
id="$(place KB-01 2)"
during="$(stock KB-01)"
[ "$during" -eq $((before - 2)) ] || fail "stock should drop to $((before - 2)) while reserved, got $during"
wait_state "$id" CANCELLED
after="$(stock KB-01)"
[ "$after" -eq "$before" ] || fail "stock not released: before=$before after=$after"
api GET "/api/orders/$id" | jq -e '[.history[].to] == ["INVENTORY_RESERVED","INVOICED","COMPENSATING","CANCELLED"]' >/dev/null \
  || fail "unexpected compensation history for $id"
api GET "/api/orders/$id" | jq -e '.cancel_reason | length > 0' >/dev/null || fail "missing cancel reason"
pass "order $id CANCELLED, KB-01 stock restored ($before)"

echo "== 4. stock rejection (not enough monitors)"
api PUT /api/admin/failure '{"stage":""}' >/dev/null
id="$(place MON-01 999)"
[ "$(api GET "/api/orders/$id" | jq -r .state)" = "CANCELLED" ] || fail "oversized order must be cancelled"
pass "order $id CANCELLED at reservation"

echo "ALL SMOKE CHECKS PASSED"
