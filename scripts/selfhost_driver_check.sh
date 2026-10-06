#!/usr/bin/env bash
# selfhost_driver_check.sh — type-check every compiler/*_run.fern
# driver, not just the compiler they share modules with.
#
# WHY THIS EXISTS. `make check-sources` runs `bin/fern -check
# compiler/fern.fern`, which reads the compiler's own import tree —
# and the 47 auxiliary drivers are not in it. They import the same modules, so
# deleting or renaming anything they use leaves a dangling reference that
# `-check fern.fern` calls clean.
#
# That is the third instance of this shape. The target's own comment records
# the first two: the wildcard-arm ratchet that drifted red on main with nothing
# to report it (#7570), and the Fern fixtures living as Go string literals
# outside the tree (#9805). The third was #9969 deleting `semsource.modes_differ`
# while `semsource_census_run.fern` still called it — `-check fern.fern` passed,
# and four `internal/e2eselfhost` tests went red forty minutes into a CI round
# because they are what builds that driver.
#
# Each driver is a whole-program check, so this is the cheapest place to catch
# it: seconds here against a CI round there.
#
# THE TWO THAT NEED -embed. playground_run and semsource_census_run call
# `__fern_assets()`, which is an error unless the compiler was given an embed
# directory — the same `-embed internal/stdlib` their own tests pass. Without
# it they fail here for a reason that is not a type error, which would make the
# gate cry wolf and then be switched off.
set -euo pipefail

cd "$(dirname "$0")/.."

fern=./bin/fern
[ -x "$fern" ] || { echo "selfhost_driver_check: no $fern; run make build first" >&2; exit 1; }

# The drivers whose sources call __fern_assets(); they get the stdlib as their
# embed directory, exactly as their tests do.
needs_embed() {
  case "$1" in
  compiler/playground_run.fern | compiler/semsource_census_run.fern) return 0 ;;
  *) return 1 ;;
  esac
}

# One check per driver, in parallel: each is a whole-program check that reads
# the compiler's closure again, ~3 s each and 47 of them, so the serial loop
# was the Lint lane's longest step. A failing driver prints its name and the
# checker's output; xargs exits 123 when any invocation failed.
check_driver() {
  driver=$1
  if needs_embed "$driver"; then
    out=$("$fern" -check -embed internal/stdlib "$driver" 2>&1) || {
      printf 'selfhost_driver_check: %s\n%s\n' "$driver" "$out" >&2
      return 1
    }
  else
    out=$("$fern" -check "$driver" 2>&1) || {
      printf 'selfhost_driver_check: %s\n%s\n' "$driver" "$out" >&2
      return 1
    }
  fi
}
export -f check_driver needs_embed
export fern

drivers=(compiler/*_run.fern)
checked=${#drivers[@]}
failed=0
printf '%s\n' "${drivers[@]}" |
  xargs -P "$(nproc)" -n 1 bash -c 'check_driver "$0"' || failed=1

# A glob that matched nothing would leave checked at 0 and report success, so
# the count is asserted rather than printed: this gate has no other way to tell
# "every driver is clean" from "no driver was looked at".
if [ "$checked" -lt 40 ]; then
  echo "selfhost_driver_check: only $checked driver(s) found under compiler/*_run.fern — the glob matched nothing like the expected set, so this gate checked nothing" >&2
  exit 1
fi

if [ "$failed" -ne 0 ]; then
  echo "selfhost_driver_check: some of the $checked drivers do not type-check (named above)" >&2
  exit 1
fi

echo "selfhost driver gate: $checked drivers type-check"
