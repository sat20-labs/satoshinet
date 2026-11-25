#!/bin/zsh
set -euo pipefail

variant="${1:-}"
timeout_pattern='^[1-9][0-9]*$'
if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
  print -u2 "usage: satoshinet-test.sh <all|blockchain|contract|dkvs|e2e> [timeoutMs]"
  exit 2
fi
case "$variant" in
  all) default_timeout_ms=3600000 ;;
  blockchain|contract|dkvs|e2e) default_timeout_ms=600000 ;;
  *)
    print -u2 "unsupported SatoshiNet test variant: $variant"
    exit 2
    ;;
esac
timeout_ms="${2-$default_timeout_ms}"
if [[ ! "$timeout_ms" =~ $timeout_pattern ]]; then
  print -u2 "timeoutMs must be a positive integer in milliseconds: $timeout_ms"
  exit 2
fi
cd -- "${0:A:h:h}"
workdir="$(pwd -P)"
if ! GO_BIN="$(command -v go)"; then
  print -u2 "Go executable not found on PATH"
  exit 2
fi
GO_BIN="${GO_BIN:A}"
module="$("$GO_BIN" list -m)"
if [[ "$module" != "github.com/sat20-labs/satoshinet" ]]; then
  print -u2 "expected SatoshiNet module, got: ${module:-<none>}"
  exit 2
fi
if [[ "$variant" == all ]] && ! command -v python3 >/dev/null; then
  print -u2 "python3 is required to summarize Go test events"
  exit 2
fi

umask 077
log_dir="${TEST_RUN_LOG_ROOT_OVERRIDE:-$workdir/.cache/script-logs/test-runs}/satoshinet-test"
mkdir -p -- "$log_dir"
log_dir="$(cd -- "$log_dir" && pwd -P)"
started_epoch="$(date +%s)"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$-$variant"
log_file="$log_dir/$run_id.log"
status_file="$log_dir/$run_id.status"
tail_file="$log_dir/$run_id.tail"
stderr_file="$log_dir/$run_id.stderr"
case_summary=""
: > "$log_file"
: > "$tail_file"

# Publish the links once at startup. A previous run finishing later only
# updates its own files. Serialize startups so all links select the same run.
publish_latest() {
  local fd suffix
  zmodload zsh/system
  : >> "$log_dir/.publish.lock"
  zsystem flock -f fd "$log_dir/.publish.lock"
  for suffix in log tail status; do
    ln -s -- "$run_id.$suffix" "$log_dir/latest.$suffix.$$"
    mv -f -- "$log_dir/latest.$suffix.$$" "$log_dir/latest.$suffix"
  done
  print -r -- "$run_id" > "$log_dir/latest.run.$$"
  mv -f -- "$log_dir/latest.run.$$" "$log_dir/latest.run"
  zsystem flock -u "$fd"
}

# Stream the JSONL file once. Count leaf tests across all four invocations.
# Keep a parent failure if it failed independently of its children.
summarize_cases() {
  python3 - "$log_file" <<'PY'
import json
import sys
from collections import Counter

rank = {"skip": 0, "pass": 1, "fail": 2}
results = {}
parents = set()
failed_parents = set()
with open(sys.argv[1], encoding="utf-8") as events:
    for line in events:
        event = json.loads(line)
        test, action = event.get("Test"), event.get("Action")
        if not test or action not in rank:
            continue
        package = event["Package"]
        key = (package, test)
        if key not in results or rank[action] > rank[results[key]]:
            results[key] = action
        while "/" in test:
            test = test.rsplit("/", 1)[0]
            parents.add((package, test))
            if action == "fail":
                failed_parents.add((package, test))

counts = Counter(action for key, action in results.items()
                 if key not in parents or (action == "fail" and key not in failed_parents))
print(f"case_pass={counts['pass']} case_fail={counts['fail']} case_skip={counts['skip']}")
PY
}

write_status() {
  local state="$1" rc="${2:-}" ended_epoch="${3:-}" duration=""
  if [[ -n "$ended_epoch" ]]; then
    duration=$((ended_epoch - started_epoch))
  fi
  {
    printf 'state=%s\nsuite=satoshinet-test\nvariant=%s\nrun_id=%s\npid=%s\n' "$state" "$variant" "$run_id" "$$"
    printf 'working_directory=%s\ntimeout_ms=%s\nstarted_epoch=%s\n' "$workdir" "$timeout_ms" "$started_epoch"
    printf 'ended_epoch=%s\nduration_seconds=%s\nexit_code=%s\n' "$ended_epoch" "$duration" "$rc"
    printf 'log_file=%s\nstatus_file=%s\ntail_file=%s\n' "$log_file" "$status_file" "$tail_file"
    if [[ "$variant" == all ]]; then
      printf 'stderr_file=%s\n' "$stderr_file"
    fi
    if [[ -n "$case_summary" ]]; then
      printf '%s\n' ${=case_summary}
    fi
  } > "$status_file.tmp"
  mv -f -- "$status_file.tmp" "$status_file"
}

report() {
  print -r -- "$1"
  if [[ "$variant" != all ]]; then
    print -r -- "$1" >> "$log_file"
  fi
}

finish() {
  local rc="$1" state=FAILED
  if [[ "$variant" == all ]]; then
    if case_summary="$(summarize_cases)"; then
      print -r -- "$case_summary"
    elif (( rc == 0 )); then
      rc=1
    fi
  fi
  if (( rc == 0 )); then
    state=PASSED
  elif (( rc >= 128 )); then
    state=ABORTED
  fi
  if [[ -s "$stderr_file" ]]; then
    print -u2 -r -- "[test-run] Go diagnostics: $stderr_file"
  fi
  tail -n 200 -- "$log_file" > "$tail_file"
  write_status "$state" "$rc" "$(date +%s)"
  report "[test-run] state=$state exit_code=$rc status=$status_file log=$log_file"
  return "$rc"
}

write_status RUNNING
trap 'finish $? || exit $?' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
publish_latest
report "[test-run] state=RUNNING run_id=$run_id pid=$$ status=$status_file log=$log_file"
go_test_timeout="-timeout=${timeout_ms}ms"

run_in() {
  local directory="$1"
  shift
  (
    cd -- "$directory" || exit "$?"
    if [[ "$variant" == all ]]; then
      exec "$@" >> "$log_file" 2>> "$stderr_file"
    fi
    exec >> "$log_file" 2>&1
    printf '+ cd %q &&' "$PWD"
    printf ' %q' "$@"
    printf '\n'
    "$@"
  ) || exit "$?"
}

run() {
  run_in "$workdir" "$@"
}

case "$variant" in
  all)
    # Enable per-case limits for the in-process contract boundary tests.
    run "$GO_BIN" test -json -tags=rpctest ./... "$go_test_timeout" -count=1
    run_in btcec "$GO_BIN" test -json ./... "$go_test_timeout" -count=1
    run_in btcutil "$GO_BIN" test -json ./... "$go_test_timeout" -count=1
    run_in btcutil/psbt "$GO_BIN" test -json ./... "$go_test_timeout" -count=1
    ;;
  blockchain)
    run "$GO_BIN" test ./blockchain/... "$go_test_timeout" -count=1
    ;;
  contract)
    run "$GO_BIN" test -tags=rpctest ./contract/... "$go_test_timeout" -count=1
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
    run "$GO_BIN" test -tags=rpctest,legacy_evm_e2e . "$go_test_timeout" -count=1
    ;;
esac
