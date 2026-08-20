# The register half: freeing a builtin string[]'s view element boxes

`docs/rc-log/2026-08-20-builtin-strarr-element-reclaim.md` credited
`s.split(sep)` / `s.lines()` results (the `SARRB:` class) and moved wasm only.
This is the other half. 400 rounds of the churn harness, a pair of compilers
from the same commit per column:

| shape | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| `base.split("-")` (18 parts) | 172800 → **0** | 172800 → **0** | 67200 (unchanged) |
| `base.lines()` (1 line) | 9600 → **0** | 9600 → **0** | 9600 (unchanged) |
| both in one frame | 182400 → **0** | 182400 → **0** | 76800 (unchanged) |

Zero, not "nearly zero": on the register backends the element boxes were the
*entire* remaining leak for these shapes. 172800 / 400 rounds = 432 bytes, which
is exactly 18 parts × the 24-byte box.

## Cause

The credit already fired on the register backends — the exit sweep emitted
`__fern_str_arr_free` where main emitted `__fern_arr_dec`, visible in the emitted
asm — and reclaimed nothing. Their `split` yields zero-copy VIEWS: a 24-byte box
over the source's bytes stamped with the immortal rc sentinel, and
`__fern_str_arr_free`'s per-element `__fern_str_free` skips an immortal rc **by
contract**. That skip is not a bug to route around; it is what stops a view
freeing bytes it does not own.

## Fix

`__fern_str_arr_view_free` — `__fern_str_arr_free` with the per-element call
changed to `__fern_str_view_free`, which frees the 24-byte box alone when the rc
is immortal and tail-jumps to `__fern_str_free` otherwise. Bodies in
`asm_ir.fern` and `asm_arm64_ir.fern`, `has_need`-gated like `arrarr_free` so a
binary that never reclaims a builtin string[] does not carry a body whose inner
`call __fn___fern_str_view_free` the negative asm-grep contracts would see.
Mapped on wasm to `$__fern_arr_dec_ptr`, the same helper as its sibling — wasm's
split copies, so there are no view boxes there.

irlower picks it through `strarr_free_helper(s, i)` at the two whole-array
release sites (exit sweep, loop-rebind store) and `strarr_elem_free_helper` at
the one per-element site (`a = a.with(i, v)`), each keyed on the slot's
`strarr_builtin` flag. **Only this class may take it.** An immortal rc makes a
second holder uncounted and undetectable, so the warrant cannot come from the
count — it comes from the classification: the runtime made these boxes, stored
them into this array and nowhere else, and the slot is proven non-escaping.

## The traps this set

**Two need-seeding sites, not one.** The x86-64 and arm64 backends each scan
call ops for helper needs (`asm_ir.fern`, `asm_arm64_ir.fern`). Seeding only the
x86 one left arm64 emitting the CALL with no BODY — the gcc-assembled leg failed
at link with `undefined reference to __fn___fern_str_arr_view_free`, which is at
least loud.

**The arm64 NATIVE path is a third gate, and the e2eselfhost arm64 leg does not
cover it.** That leg assembles through cross-gcc; `-target arm64-linux` on the
CLI assembles in-process (`arm64_native` + `elf.fern`) and reports a missing
runtime label as `in-process assembler refused 2: label:…`. A compiler built
before the arm64 need seeding passed the whole gcc-based leg and refused every
program on the native path. `TestFernFixturesSelfHostArm64` is what covers it —
and the CLI probe that found it took seconds.

**A new `.need("x")` root must be listed in `all_runtime_need_roots`.** Its own
comment says an omission links a module against an undefined `__fern_x` under
`-per-module`, caught by the per-module link test rather than silently.

## What is witnessed, and what is not

Witnessed at fault level, from the predecessor and still holding here (now with
teeth on the register backends, where something is finally freed): the TYPE gate.
A compiler with the method name matched and the binding-site type confirmation
dropped over-releases.

Witnessed by measurement: the release happens — every heap case sits at 0 on both
register backends with a 4096-byte ceiling, and at 172800 / 9600 / 182400 without
the change.

**Not** witnessed at fault level: the NON-ESCAPE half. Dropping
`strarr_unsafe_for`'s verdict for this class does change emission — `first_of`
(an element returned out of the frame) goes from `__fern_arr_dec` to the element
walk — so the guard decides something real. But no probe faults under that
compiler, including one whose decoys are slices, sized to land in the same
24-byte class a freed element box does. The escape cases in the suite are
correctness gates, not fault witnesses; the half they rest on is the `SARR:`
class's, unchanged.

Three shapes probed for hazards the view free could have introduced, all clean on
all three backends and both compilers:

- **Source in `.rodata`** (`"alpha-beta-gamma".split("-")`) — the element views
  point outside the arena, which the box free's heap-range guard declines.
- **Separator absent** — one part covering the whole source. Had the runtime
  handed back the source's own box rather than a fresh view over it, freeing it
  would have destroyed `base` and its scope-exit dec would have double-freed.
- **Mixed array** — `parts.with(1, w("…"))` replaces one view with a fresh
  string, so the walk meets both kinds and has to reclaim each exactly once.

## Next lead

wasm still strands 67200 on the 18-part split (168 bytes/round). It is not the
element boxes — those are gone there, which is what the predecessor bought — and
`lines` (one element) sits at 9600 on all three backends, so whatever remains
scales with something other than element count. Unmeasured; that is the next
thing to pull on in this shape.
