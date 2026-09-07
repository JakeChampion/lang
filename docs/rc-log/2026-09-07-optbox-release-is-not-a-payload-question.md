# Releasing an Option / Result box is not a payload question (#8806, #8811)

*2026-09-07* — `examples/self_host/irlower.fern`, all backends. The self-host's
box half of native's #8405, and the payload-kind gap #8410 left open.

## The measurement

`bin/fern-selfhost -target x86-64-linux`, `FERN_LEAKCHECK=1`, three calls each
except where noted:

| program | before | after |
| --- | --- | --- |
| `match (g(i))`, `g: (i32) -> Option[IoError]` | 3 allocs / **0** frees, 120 B | **3 / 3, 0 B** |
| the same through a written `var e: Option[IoError] = g(i)` | 3 / **0**, 120 B | **3 / 3, 0 B** |
| the identical program over `Option[i32]` | 3 / 3 | 3 / 3 |
| `var a: Option[i32] = f(i);` never matched | 3 / **0**, 120 B | **3 / 3, 0 B** |
| `var a: Option[IoError] = g(i);` never matched | 3 / **0**, 120 B | **3 / 3, 0 B** |
| 200 x `match (env(k))` | 201 / **0** | **201 / 200, 32 B** |
| 200 x open-and-close rounds | 601 / **0** | **601 / 400** |
| the same with the open's Result bound to a `var` first | 601 / **0** | **601 / 400** |
| `conformance/cases/alloc_flat_read_chunk`, self-host | `scales` | **`constant`** |

The last row is the point of the exercise. #8398 rewrote that case's assertion
once native reclaimed the boxes — its old one allowed the wide round twice the
narrow round's fresh bytes *specifically to tolerate the per-call box*, so the
leak was its own denominator — and the self-host has failed it since.

## Three causes, every one of them an admission

None of this needed a runtime change or a new release. The self-host's I/O
helpers are generated **Fern source** (`asmcore.rt_src_*`), so their `Ok(..)` /
`Some(..)` / `None` build ordinary rc=1 boxes through the same `opt_make` every
user `Option` uses; `emit_scalar_enum_box_free` — a null-safe dec plus a slot
zero — was already the release three other sites emit.

- **`match_scrut_owns_fresh_result_box` named one builtin.** #8410 admitted
  `Writer.write` and deliberately stopped there, because an open-and-close round
  strands three boxes and admitting one of them cannot be told from admitting
  none. It now admits the two spellings `lower_call` / `lower_call_method`
  intercept: any RESOURCE METHOD behind `resource_method_opt_ret_type`'s own
  receiver gate, and any bare-ident BUILTIN whose Option / Result return comes
  from `builtin_opt_ret_type` and whose name no user function shadows.
  Reading the gates off the call lowering is what keeps this from claiming a
  user `.write()` or a user producer — the latter is `hoist_call_scrutinees`'
  business, which turns a direct call to one into a `var` first. The builtin
  table moved out of `LowerState.opt_ret_type` into `builtin_opt_ret_type` so the
  rc side and the type side read ONE table; a second list here would drift from
  what `lower_call` actually intercepts, and the drift is invisible — a release
  emitted for a call that was lowered as something else entirely.

  The same builtin gate also admits the BINDING form (`var r: Result[Reader,
  IoError] = open_reader(p); match (r)` in `fresh_opt_box_init`): it is the same
  box out of the same helper, and fixing only the spelling that happens to be a
  scrutinee is the partial admission this family already refused once. A BINDING
  is not an anonymous scrutinee, though — see the payload gate below.

- **The two consumed-Option analyses are keyed on the PAYLOAD kind.**
  `consumed_scalar_enum_frees` wants a scalar payload and
  `consumed_rcpayload_option_frees` a leak-safe array / fresh string / nested
  scalar Option, because each releases the payload as well as the box. A
  payload in neither — a struct, an `IoError`, a `Reader` — was in no set at all,
  so the BOX went with it. `consumed_optbox_frees` is the box-only third: the
  same gates (one consuming match, dead after, single-owner, non-escaping) and a
  release that stops at the box. It is DISJOINT from both by refusing whatever
  they admit, and it is deliberately not fed to `enum_donor_reuse_sites` —
  the donor path writes over a box's slots without releasing them, which is
  sound only when they really are scalars.

- **A binding nothing looks at had no site to hang a drop on.**
  `precise_drop_names` places its drop after the LAST USE and requires one
  (`last > i`), and every consumed-* analysis requires a consuming match. A
  local with neither — `var a: Option[i32] = f(1);` and nothing else — was the
  cheapest thing a program can do and the one shape that kept its block.
  `consumed_optbox_frees` takes that too, at both function and block level,
  freeing right after the declaration. It admits EVERY payload kind there,
  which needs no disjointness argument: nothing else claims a local that is
  never mentioned again.

A fourth, smaller piece: `precise_drop_names` gained the box-only candidate kind
so a top-level local whose consuming match is one block deeper is reclaimed too
— neither `consumed_optbox_frees` (it looks for a top-level scrutinee at
function scope) nor the dead branch (the enclosing `if` is a use) sees that one.

## The payload gate, and the 26 tests that wrote it

A box-only release is sound for every payload kind ON A SCRUTINEE, whose box is
an anonymous `$mscrut` slot no analysis knows about. It is NOT sound on a
BINDING, and the first version of this change got that wrong in both halves.

`emit_scalar_enum_box_free` zeroes the slot. A local whose payload has a reclaim
class of its own — the OPTSTR / OPTARR / OPTARRARR / OPTAARR / OPTARRERR /
OPTTUP / OPTOPT* / OPTSTRUCT credits — is deep-dropped LATER, and a zeroed slot
makes that drop find null and release nothing. So the box-only free did not add
a small win on top of a payload leak; it REPLACED a working deep drop with a
40-byte one. 26 `internal/e2eselfhost` tests went red, all of them named for the
payload kind they cover: `UnmatchedOptStr`, `OptArrRebindUnmatched`,
`OptTupNestedMatch`, `UnmatchedOptopt`, `TupStructFieldReclaim`, both
`LeakMatrix` legs. The same run on the untouched file is 0 red, so the control
is clean.

`optbox_shallow_payload_ok` is the gate that came out of it: a payload spelling
is admitted only when nothing has a class for it — a scalar, an enum, an opaque
nominal like `IoError` / `Reader` / `FileStat` — which is exactly the set whose
box was kept in the first place. A string, a string[], any array, a tuple, a
nested Option / Result, a Map and a declared struct are all REFUSED, on the
error payload as well as the success one, and an unannotated construction is
refused for want of a spelling to read. `Option[string]` from `env` bound to a
var therefore keeps its box, unchanged from before.

## Why box-only is sound for the kinds that pass that gate

The box is one block; a payload that is itself a box has its own owner or its
own pre-existing leak, and `__fern_rc_dec` on the box never walks into it. So
this class can turn a two-block leak into a one-block leak and can never turn a
leak into a dangle. Concretely: a FAILING write / close / open still strands the
`IoError` it built, one block per failing call against one per call before, and
`read_chunk`'s success payload belongs to the arm binding (#8402) — a shallow
box free never touches it, which is why #8402's probes read identically either
way.

Native's #8398 had to check a second fact here, and the self-host does not: over
there a payloadless arm allocating below the enum's uniform size would push a
short block onto a bigger freelist, corruption whose only symptom is
`live_bytes` going NEGATIVE. The self-host frees through `__fern_rc_dec`, which
reads the size out of the block's own header, so the arm sizes are not a second
record that can disagree. Checked anyway: every measurement above is a positive
`live_bytes`.

## What is still open

- **`__fern_open_res` and its fs-bundle siblings strand their NUL-terminated
  path buffer on the SUCCESS path**, and the leak scales with the path: an
  `open_reader` round leaks 8,032 bytes over 200 rounds at a 9-character path
  and 17,632 at a 57-character one. Same shape #8402 fixed for `read_chunk`, on
  the open side, but it is a whole family of runtime leaves rather than an
  irlower admission — filed as **#8813**, deliberately not fixed here. The
  open-close gate names it rather than working around it: it asserts each round
  leaves exactly ONE block, so one unreleased box reads as 2 and both as 3.
- **A `string` payload reached through a BINDING.** `var r: Result[string,
  IoError] = read_file(p); match (r)` keeps its box, because
  `optbox_shallow_payload_ok` refuses a string: the OPTSTR credit owns that
  payload and the box-only free would zero the slot in front of it. The
  SCRUTINEE spelling of the same call is released. Closing it needs a release
  that deep-drops the payload rather than one that stops at the box, which is
  `consumed_rcpayload_option_frees`' shape and wants the freshness proof it
  requires — these builtins are not in the OPTFRESH registry that carries it.
- The **`?` binding form** (`var s: string = r.read_chunk(n)?`) is a different
  site with its own credit, untouched, exactly as #8402 recorded.
- The **fresh `IoError` a FAILING write / close / open builds inside its box**,
  which the shallow release does not reach: one block per failing call, against
  one per call before. Deep-dropping it needs the variant walk plus a proof the
  `Some` arm's binding does not outlive it.

## Traps

- **A binding is not a scrutinee.** The section above is the whole of it: the
  release that is unconditionally safe on an anonymous match scrutinee replaces
  a deep drop when the box has a name. Nothing in the probe set for this change
  caught that — the 26 tests that did are elsewhere in the package, which is why
  the targeted families and not just the new file are the gate.
- **The dead-binding shape is scope-sensitive, and the obvious probe misses
  it.** `precise_drop_names` runs on `fn.body` only, so a probe that puts the
  binding in a `while` body is measuring the BLOCK-level path and a probe that
  puts it at function top level is measuring the function-level one. The first
  version of `TestSelfHostOptBoxReclaimX86_64` had only the loop form and passed
  a fix that reached neither.
- **A per-call constant hides in an absolute byte count.** Every leg compares
  the same program at 20 and at 200 rounds and requires EQUAL live bytes.
- **`match_scrut_owns_fresh_result_box` must read the same gate the call reads.**
  A separate name list here would drift from what `lower_call_method` actually
  intercepts, and the drift is invisible: the release is emitted for a call that
  was lowered as something else entirely.

## What gates it

- `conformance/cases/alloc_flat_read_chunk` through the self-host — `constant`
  now, `scales` before. The whole point.
- `TestSelfHostOptBoxReclaimX86_64` (six legs: the non-scalar payload as a
  scrutinee and as a binding, the dead binding at both payload kinds, an arm
  that `return`s, and the top-level binding with a nested match) and
  `TestSelfHostIoResultBoxReclaimX86_64` (the open-and-close slope as a scrutinee
  and through a binding, `read_chunk` round-count independence).
- Unmoved: `TestSelfHostPrintWriteBoxReclaimX86_64`,
  `TestSelfHostOwnedPayloadReclaim/ShortRead/Hazards`,
  `TestSelfHostCopyingBuiltinArgX86_64`, `TestSelfHostFeatureCensus`, the
  complexity ratchet.

Each piece was reverted in isolation, and each has a leg that goes red on its
own:

| reverted | what fails |
| --- | --- |
| the builtin-family admission in `match_scrut_owns_fresh_result_box` | `alloc_flat_read_chunk` self-host, back to `scales`; all three `IoResultBox` legs |
| `consumed_optbox_frees`' CONSUMED half | `OptBox/nonscalar_payload_scrutinee`, `/nonscalar_payload_bound`, `/returning_arm`, `IoResultBox/open_close_bound` |
| `consumed_optbox_frees`' DEAD half | `OptBox/dead_binding_scalar` and `/dead_binding_nonscalar` |
| `is_optbox` in `precise_drop_names` | `OptBox/nested_match_fn_level` alone |
| the return-path `#b` arming | `OptBox/returning_arm` alone — the round that leaves the function from inside its matched arm |
| the builtin arm of `opt_box_init_type` | `IoResultBox/open_close_bound` alone |
| `optbox_shallow_payload_ok` | **107 subtests across 26 `internal/e2eselfhost` tests** — the payload kinds whose deep drop the zeroed slot starves |
