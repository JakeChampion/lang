#!/usr/bin/env bash
#
# native-convergence freeze gate
# ------------------------------
# Derives the mechanically-checkable freeze preconditions from docs/NATIVE-
# CONVERGENCE.md and reports each one's live state. Run with `make freeze`.
#
# WHY THIS EXISTS. The preconditions lived only as prose checkboxes on the
# tracker issue (#4451), and prose does not get re-checked when the code moves.
# Audited 2026-08-02, every stale claim pointing the same way — more pessimistic
# than reality:
#
#   - "#3457 is blocked on the wasm component-model chain (#4315-#4320). This is
#     the sole substantial remaining gate." #3457 and all six of #4315-#4320
#     were closed. The gate had already opened.
#   - Goal 2's remaining deltas listed "enum / Map / closure / tuple pointer
#     fields", which docs/SELFHOST-PERCEUS-REUSE.md's own correction header had
#     already superseded.
#   - #5314, listed as an open RC blocker, was closed.
#
# Three trackers drifting the same direction is a systems problem, not three
# oversights: a human has to notice that a condition changed, and nobody is
# assigned to notice. So anything derivable is derived here instead. The
# irreducibly-human ones (is the Perceus port at parity?) are printed as
# UNVERIFIABLE with a pointer, rather than silently assumed either way.
#
# EXIT STATUS. 0 unless a derivable precondition has REGRESSED — an AST emitter
# comes back, a backend loses its IR-or-error routing, the checker-codes filter
# returns. Those are real losses of ground, so they fail. UNVERIFIABLE is not a
# failure: it means the condition needs a human, and saying so beats guessing.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

green=0
unverifiable=0
regressed=0

ok()   { printf '  \033[32mGREEN\033[0m        %s\n' "$1"; green=$((green + 1)); }
huh()  { printf '  \033[33mUNVERIFIABLE\033[0m %s\n' "$1"; unverifiable=$((unverifiable + 1)); }
bad()  { printf '  \033[31mREGRESSED\033[0m    %s\n' "$1"; regressed=$((regressed + 1)); }

echo "native-convergence freeze preconditions (docs/NATIVE-CONVERGENCE.md, tracker #4451)"
echo

# --- 1. Goal 2 / Perceus parity -------------------------------------------
# Not derivable: "at parity" is a judgement over inc/dec insertion, borrow
# inference, drop specialisation and reuse analysis. What IS derivable is that
# the reuse machinery exists and the fixpoint exercises it, so a regression
# that deleted it would show up here rather than only in a doc.
echo "1. Roadmap goal 2 — Perceus port at parity in the self-host compiler"
if grep -q 'struct_fields_reusable_cross' examples/self_host/irlower.fern 2>/dev/null; then
  ok "constructor-reuse admission present (struct_fields_reusable_cross)"
else
  bad "struct_fields_reusable_cross is gone — reuse admission was removed?"
fi
huh "parity itself — see docs/SELFHOST-PERCEUS-REUSE.md §3 for the live delta list"
echo

# --- 2. #3451 / #3457 — per-module epic -----------------------------------
# The doc's stated end condition for this precondition is the emitter deletion,
# which is a file-existence check. #3451 stays open on step 6 (#3458,
# incremental codegen / per-module object cache) — build performance, and
# self-host-only, so it does not bear on whether native has stopped being where
# features land. That distinction is the reason this precondition is scored on
# the emitters rather than on the epic being closed.
echo "2. #3451 / #3457 — per-module compilation, ending with the AST emitters deleted"
emitters_present=""
for f in asm.fern asm_arm64.fern wasm.fern; do
  [ -f "examples/self_host/$f" ] && emitters_present="$emitters_present $f"
done
if [ -n "$emitters_present" ]; then
  bad "legacy AST emitter(s) present:$emitters_present (#3457 deleted all three)"
else
  ok "asm.fern / asm_arm64.fern / wasm.fern all deleted"
fi
# Every backend must route IR-or-error, i.e. expose the *_or_error entry point
# that replaced the silent AST fall-through.
for pair in "asm_ir.fern:emit_module_or_error" \
            "asm_arm64_ir.fern:emit_module_or_error" \
            "wasm_ir.fern:emit_module_mode_or_error"; do
  f="${pair%%:*}"; fn="${pair##*:}"
  if grep -q "function $fn" "examples/self_host/$f" 2>/dev/null; then
    ok "$f routes IR-or-error ($fn)"
  else
    bad "$f has no $fn — the IR-or-error routing is gone?"
  fi
done
echo

# --- 3. Checker-codes filter empty ----------------------------------------
# The differential must compare the UNFILTERED code sets. A reintroduced
# allowlist would silently shrink the comparison, which is exactly the failure
# this precondition exists to prevent.
echo "3. Checker-codes differential compares unfiltered code sets"
# Match CODE, not prose. The first cut of this check grepped raw lines and
# immediately fired on the two comments in self_host_checker_codes_test.go that
# explain the filter is deleted — a gate whose first run is a false positive is
# worse than no gate, because the next person learns to ignore it. Strip //
# comments before matching. (Go string literals containing "//" would confuse
# this, but the identifier never appears in one.)
hits="$(grep -rl --include='*.go' 'selfHostImplementedCodes' internal/ 2>/dev/null \
  | while read -r f; do
      sed 's://.*::' "$f" | grep -q 'selfHostImplementedCodes' && echo "$f"
    done || true)"
if [ -n "$hits" ]; then
  bad "selfHostImplementedCodes is referenced in code again: $(echo "$hits" | tr '\n' ' ')"
else
  ok "no selfHostImplementedCodes filter in code (comments referencing it are fine)"
fi
echo

# --- 4. SH-057-class semantics --------------------------------------------
# Mutable scalar captures across closure boundaries, in EVERY engine. #2850 was
# closed once on the compiled path while interp.fern still disagreed, so the
# check is that each engine carries the machinery, not that an issue is closed.
echo "4. SH-057-class semantics (mutable scalar capture) closed in every engine"
if grep -q 'VCellI' examples/self_host/interp.fern 2>/dev/null; then
  ok "self-host interpreter has by-reference scalar capture (VCellI/VCellF)"
else
  bad "interp.fern lost its scalar-capture cells — SH-057 regressed?"
fi
if grep -rq 'Cell\[i32\]\|cellify' examples/self_host/interp.fern 2>/dev/null; then
  ok "capture cells are wired (cellify_env)"
else
  bad "interp.fern has cells but no cellify path"
fi
echo

printf 'summary: %d derived green, %d needing human judgement' "$green" "$unverifiable"
if [ "$regressed" -gt 0 ]; then
  printf ', \033[31m%d REGRESSED\033[0m\n' "$regressed"
  echo
  echo "A previously-green precondition has gone backwards. That is a real"
  echo "regression in the convergence position, not a bookkeeping change:"
  echo "fix it, or amend docs/NATIVE-CONVERGENCE.md and this gate together."
  exit 1
fi
printf '\n\n'
echo "Update #4451 from this output rather than from memory. What this cannot"
echo "settle — precondition 1's parity question — is the only part that should"
echo "still be argued in prose."
