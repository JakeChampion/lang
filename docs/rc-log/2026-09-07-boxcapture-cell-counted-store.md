# the capture cell owns its element, so its store retains and releases (#8441)

`BoxMutatedCaptures` rewrites a captured-and-assigned local into a shared
one-element array cell and every `x = v` into `x[0] = v`
(`internal/closureconv/boxcapture.go`). That store went through the raw
in-place path: no retain of the new value, no release of the old. One
superseded buffer stranded per rebind, linear in iterations rather than in
live data — 3.2 MB per 100k-iteration loop, and unbounded.

## What the old comment said the guard was for

> The superseded element is deliberately NOT released there — a captured value
> is reclaim-ineligible (`rc.freeEligible=false` skips it at overwrite AND
> exit, keeping the two balanced), so the store safe-leaks the old pointer
> rather than risking an over-release of a value the outer scope still holds.

The over-release risk is real and is witnessed below. The rest of the sentence
was not: the cell is NOT reclaim-ineligible. `computeFreeEligible` sees it as an
ordinary array-literal-initialised local (the Index-target assign is invisible
to the analysis — its `*ast.Assign` arm only records Ident targets), so the exit
sweep already emits the DEEP `__fern_drop_arr_str` for it, which walks the
element on the cell's last reference. **The exit side already asserted that the
cell owns its element; only the store failed to maintain it.**

## The fix

`emitBoxedCellStore` (`internal/ir/ir.go`) is the `Cell.set` shape with an array
spelling, and carries the same bookkeeping:

1. stash `&cell[0]`, so the load of the old element and the store of the new one
   hit one address;
2. evaluate the new value and retain it when `needsRcIncOnAlias` — value first,
   for `emitCellSet`'s reason (`s = s + "x"` reads the slot inside the value
   expression);
3. release the superseded element through `dropStructField`, the ladder a
   container slot's replacement already takes.

A closure element takes that ladder's flat `__fern_rc_dec` (`dropFnNameFor`
declines a `FuncType`), so superseding an element on a cyclic graph decrements
without re-entering a closure release — the recursion #8637 traced does not come
back.

Second half, `genClosureDropThunk` (`internal/ir/rc_insert.go`): a `string[]`
capture was freed with the buffer-only `__fern_arr_dec`, so the cell's LAST
element was stranded even once the store was counted. The array arm now mirrors
`dropStructField`'s and walks with `__fern_drop_arr_str`.

Both halves are needed, and each is witnessed. The store alone leaves the final
element (32,000 -> 32 on the repro). The thunk alone reclaims only that element
(32,000 -> 31,968) and OVER-RELEASES the moment the cell's last element is an
alias its writer still holds — `s = a; return f() + a.len();` reports
`use-after-free (touched a quarantined block)` with the thunk change and nothing
else, and is clean with both.

## Measured

500-round loops, `-sanitize`, live bytes at exit. There is no wasmtime on this
machine, so the wasm column is the census counters read straight out of linear
memory after `main` under node's WASI. That harness reproduces the pinned 1600
for the cycle case on the unpatched compiler, which is what makes its numbers
comparable; the real runner still has the last word in CI.

| case | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| `closure_capture_rebind_churn_free` | 16000 → **0** | 16000 → **0** | 16000 → **0** |
| `closure_capture_rebind_alias_not_over_released` | 16000 → **0** | 16000 → **0** | 16000 → **0** |
| `closure_cycle_leaks_without_crashing` | 1600 → 5600 | 1600 → 5600 | 1600 → 4000 |

The issue's own 100k loop: 3,200,000 → 0 on x86-64.

The self-host sources do contain boxcapture cells — the two `bin/fern-selfhost`
builds differ byte for byte — but they are not where the driver's residue is:
**driver 108,672 → 108,560 B (−112, +5 frees)** on a small input, output
byte-identical, and unmoved on `examples/regex_captures.fern`.

## The cycle number went UP, and that is the fix working

`g = f` closes `cell → pair → env → cell`. Every member of a cycle is
uncollectable, so three blocks a round is the honest count. The old 1600 was one
block a round because the cell's edge to the closure was UNCOUNTED: the pair and
the env were freed at `f`'s own release while the cell still named them. The
smaller number was bought by releasing inside a live cycle. Exit stays 0, the
sweep does not recurse, and #8637's contract — leak, do not crash — holds.

## The guard is witnessed

Take the retain out and keep the release, and
`closure_capture_rebind_alias_not_over_released` reports
`fern-sanitizer: use-after-free (touched a quarantined block)`, exit 124: the
first supersede frees `a`'s buffer, the second round reads it. That is exactly
what the raw store was protecting against, and it is why the retain is the
load-bearing half rather than an optimisation.

The case only witnesses it because `mk` interpolates its argument. Folded to a
literal the string is the immortal static sentinel, every rc helper no-ops, and
all three builds — leaking, over-releasing and correct — read `allocs=2 frees=2
live_bytes=0`. The first draft of the case was vacuous that way.

## Trap

`-sanitize` on the natives DOES carry the use-after-free quarantine; the probe
that suggested otherwise was a 4-character string sitting in SSO with no heap
buffer to quarantine.

## Next lead

The `closure_*_churn` pins are still the buffer-only capture sweep in the same
thunk: the array-of-struct and Map arms there are narrower than
`dropStructField`'s, which is what the string arm just stopped being.
