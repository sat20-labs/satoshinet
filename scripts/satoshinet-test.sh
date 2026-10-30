#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
cd "/Users/yingfeng/github/satoshinet"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
source "/Users/yingfeng/mcp/local_access/scripts/commands/test-run-log.sh"

variant="${1:-}"
timeout_ms="${2:-600000}"
timeout_pattern='^[1-9][0-9]*$'
if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
  print -u2 "usage: satoshinet-test.sh <all|blockchain|contract|dkvs|e2e> [timeoutMs]"
  exit 2
fi
if [[ ! "$timeout_ms" =~ $timeout_pattern ]]; then
  print -u2 "timeoutMs must be a positive integer in milliseconds: $timeout_ms"
  exit 2
fi
if [[ ! -x "$GO_BIN" ]]; then
  print -u2 "Go executable not found: $GO_BIN"
  exit 2
fi

module="$($GO_BIN list -m 2>/dev/null || true)"
if [[ "$module" != "github.com/sat20-labs/satoshinet" ]]; then
  print -u2 "expected SatoshiNet module, got: ${module:-<none>}"
  exit 2
fi

test_run_log_init "satoshinet-test" "$variant" "$timeout_ms"
go_test_timeout="-timeout=${timeout_ms}ms"

run() {
  test_run_exec "$@"
}

run_in() {
  local directory="$1"
  shift
  test_run_exec_in "$directory" "$@"
}

case "$variant" in
  all)
    run "$GO_BIN" test ./... "$go_test_timeout" -count=1
    run_in btcec "$GO_BIN" test ./... "$go_test_timeout" -count=1
    run_in btcutil "$GO_BIN" test ./... "$go_test_timeout" -count=1
    run_in btcutil/psbt "$GO_BIN" test ./... "$go_test_timeout" -count=1
    ;;
  blockchain)
    run "$GO_BIN" test ./blockchain/... "$go_test_timeout" -count=1
    ;;
  contract)
    run "$GO_BIN" test ./contract/... "$go_test_timeout" -count=1
    ;;
  dkvs)
    run "$GO_BIN" test ./indexer/indexer/dkvs "$go_test_timeout" -count=1
    run "$GO_BIN" test . -run '^TestDKVS' "$go_test_timeout" -count=1
    run "$GO_BIN" test ./indexer/rpcserver/indexer \
      -run '^(TestDKVSLocalOnlyMiddleware|TestBindDKVSJSONBodyLimit)$' \
      "$go_test_timeout" -count=1
    run "$GO_BIN" test ./wire ./peer ./indexer/indexer ./indexer/share/indexer \
      -run '^$' "$go_test_timeout" -count=1
    ;;
  e2e)
    # Supported network-level contract suite. The suite builds its node runtime
    # with rpctest,wallet_plugin as required by the harness.
    run "$GO_BIN" test -tags=rpctest ./integration/contract_e2e "$go_test_timeout" -count=1

    # Legacy in-process EVM/Solidity E2E tests use a separate build tag.
    run "$GO_BIN" test -tags=legacy_evm_e2e . "$go_test_timeout" -count=1
    ;;
  *)
    print -u2 "unsupported SatoshiNet test variant: $variant"
    exit 2
    ;;
esac
