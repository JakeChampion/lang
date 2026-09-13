# `a = a.with(i, v)` reads the count at run time

The closing section of `2026-09-13-with-clone-supersede-release.md` asked for
one measurement before the self-host's static clone gate could be promoted to
the runtime test native uses: which alias kinds hold a count. This entry is
that measurement, the promotion, and what the measurement found on the way.

## The measurement

Thirty-odd programs, one per way a scalar array local gets a second holder,
each running `a = a.with(0, 9 + i)` a hundred times and then reading the
holder back; `fern -interp` and native x86-64 as the oracles, the self-host
x86-64 build with `FERN_LEAKCHECK=1` at emit as the subject. On main before
this change, ELEVEN of them return the wrong answer from the self-host —
the update wrote through the holder — with a clean census:

| holder | shape |
| --- | --- |
| nested-array append | `outer = outer.append(a)` |
| nested-array element store | `outer = outer.with(0, a)` |
| enum payload | `E.Hold(a)` |
| Option payload | `Some(a)` |
| callee stores the param | `mk(a)`, `function mk(xs) { return H { xs: xs } }` |
| callee returns the param | `id(a)` |
| callee returns a field | `get(h)` / `h.get()` returning `h.xs` |
| element read | `var a = outer[0]` |
| foreach over the array | `for x in a { a = a.with(…) }` |
| foreach row projection | `for row in outer { row = row.with(…) }` |
| enum payload projection | `E.Hold(xs) => { xs = xs.with(…) }` |

None is a missing retain. The static gate (`aliased_array_names_of`) credits
a bare-ident bind and a container-literal element and nothing else, so each of
these took the in-place `arr_set`. The holders in the first eight rows DO hold
a count — that is what the runtime test shows, below — and the last three are
the two projection binds (borrowed, with an ownership flag) and the foreach
reading its slot per iteration.

The shapes the static gate did credit — `var b = a`, `b = a`, a struct /
tuple / nested-array literal, a nested block, a local bound from a param or a
field — were right, at one whole-buffer clone per update: 101–102 allocations
for the hundred, every one released since the predecessor entry.

## The promotion

`lower_counted_arr_with`: for a LOCAL scalar-element array slot that holds one
counted reference (`arr_slot_shallow_release_ok`, the exit sweep's own
condition), evaluate index and value, then `__fern_rc_is_unique(slot)`: unique
writes in place; shared takes `arr_slice` over the whole length (a fresh rc 1
copy), releases the slot's reference to the original, binds the copy, and
writes into it. The next update finds the copy unique. Composed of existing
ops, so all three backends lower it unchanged.

`lower_flagged_arr_with` is the same for a slot whose ownership is the hidden
`$ownflag` — a borrowed array parameter the body rebinds, or a foreach / match
scalar projection. The count is read only when the flag says this frame owns
the reference; a borrow copies whatever its count reads, because the owner's
sole reference is not this frame's to write through. The store is the flag's
(`emit_consumed_param_store`), which releases an owned predecessor and sets
the flag. A borrowed parameter updated directly used to clone on every
iteration too (`param-direct`: 101 → 2).

Both are restricted to `slot_holds_scalar_elems`: string, struct, enum,
nested, closure, Option and tuple element arrays keep their existing arms,
whose element accounting this does not touch.

Two holders were uncounted, and each is fixed at its site rather than refused:

- `outer = outer.with(i, a)` stored `a`'s pointer with no count, the `.with`
  twin of the append arm's #4702 retain; it retains now. The nested array is a
  leak-only class, so the row is held, not released (the probe reads
  112 live bytes where it read 64 and a dangling row).
- `for x in a` over a local the body assigns iterates a hidden snapshot bound
  through the ordinary `var` ladder (`lower_foreach_snapshot`), so the loop
  reads the entry value and the body's rebind copies away from it.

And a parameter named in a scalar return (`return p[0] + p[1]`) is now
released at exit: `ret_type_cannot_hold_buffer` says an `i32` cannot be the
buffer, where the exit sweep left every such parameter alone as a possible
handback (48 live bytes per call before).

## After

Every probe agrees with the interpreter. Of the eleven wrong answers, eight
were fixed by reading the count alone, the three projection / foreach rows by
the flagged path and the snapshot. The hundred-update loops:

| shape | before | after |
| --- | --- | --- |
| `var b = a` | 101 / 101 | 2 / 2 |
| local from a borrowed param (`__bi_mul_small`) | 101 / 101 | 2 / 2 |
| local from a struct field (`BigInt.to_string`) | 102 / 102 | 3 / 3 |
| four widths, each aliased | 404 / 404 | 8 / 8 |
| borrowed param updated directly | 101 / 100, live 48 | 2 / 2 |
| holder created after each update and kept across the back edge | 102 / 102 | 101 / 101 |

The last row is the program's volume, not the compiler's: the buffer really
is shared at every update.

The compiler-sized shapes, self-host x86-64 against the same source built by
the parent commit's compiler, `FERN_LEAKCHECK=1` at emit, output byte-identical
to native in every cell:

| workload | before | after |
| --- | --- | --- |
| `od -t fL`, 48 bytes | 242,830 / 242,402, 0.21 s | 4,423 / 3,995, 0.008 s |
| `od -t fL`, 300 bytes | 4,780,491 / 4,779,367, 10.0 s | 27,634 / 26,510, 0.09 s |
| `printf '%e %g %f\n' 4e-4951 ×3` | 4,985,144 / 4,984,981, 16.5 s | 11,072 / 10,909, 0.10 s |
| `printf '%f\n'` f_range | 2,648,250 / 2,647,994, 6.9 s | 8,384 / 8,128, 0.06 s |

Native reads 14,955 on the 300-byte `od`. Live bytes are unchanged in every
row (363,848 on the 300-byte `od`): the residue the predecessor entry left is
not this shape.

## Size

Same-source linked bytes, the fifteen stock drivers built by
`TestSelfHostWarmStockDriver` under the parent commit's sources and under
this change, one toolchain (the container's gcc/ld), so the two columns are
comparable where a comparison against `.github/selfhost-driver-sizes.txt` is
not — that baseline was linked on the CI image, and every driver here reads
4.4–5.1% above it before and after alike.

| driver | parent | this change |
| --- | --- | --- |
| fern.fern | 12,044,684 | 12,048,860 (+4,176) |
| asm_load_run.fern | 8,739,004 | 8,747,276 (+8,272) |
| asm_run.fern | 7,675,892 | 7,684,164 (+8,272) |
| asm_ir_elig_run.fern | 5,798,164 | 5,806,436 (+8,272) |
| asm_ir_run / asm_modload / asm_pathprobe / irlower_run / ssa_lift_scan / wasm_ir_run / wasm_run / wasm_runio_run | | each +4,176 |
| checker_modload_run / ssa_emit_run / ssa_run | | unchanged |

At most +0.14%: the sites in the compiler's own sources that now carry the
gate are few, and the drivers that link no `.with` self-reassign are
byte-identical.

## Gates

`TestSelfHostWithCowIR{X86_64,Arm64,Wasm}` (`self_host_with_cow_ir_test.go`):
the probe set as a table, each case interpreter-checked under
`FERN_STRICT_IR=1` and its census required balanced at live 0 within a per-case
allocation bound — a hundred-update loop that allocated a hundred times fails
the bound, and an in-place write under a wrong uniqueness answer fails the
exit code. Cases whose holder is a leak-only class pin the exit code only.
Against the parent commit's lowering 27 of the 30 cases fail; the three that
pass are the controls (a sole owner, the call-scrutinee Option projection,
and the string[] exclusion).

Also green on this change: `TestSelfHost(WithAliasIR|WithCloneReclaim|
OwnArrayLifetimeNativeOracle|BorrowedWithInPlace|IRVerify)` — the first
round of which caught the capture cell, `$cell$x`, which must write through
to the closure sharing it and is excluded from both count-reading paths —
`TestSelfHostCoreutilsParity/(od|printf)` against GNU 9.4, the complexity
ratchet, `make check-sources` and `make fmt-check`.

## Found and not fixed here

- A `match` on an `Option[T[]]` LOCAL binds the payload as the element type,
  so `xs = xs.with(…)` bails with `i32.with` (#9190). The table uses the
  user-enum payload and the call-scrutinee Option form, both of which lower.
- `var b = a; … a = a.append(x)` leaks the original buffer, 2 / 1 (#9191);
  the foreach snapshot reaches the same pairing.
- An array handed out of an if-expression is credited as moved, so the
  source's updates take the static clone arm with no release: 103 / 3 (#9192).
