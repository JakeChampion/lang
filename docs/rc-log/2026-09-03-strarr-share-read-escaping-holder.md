# The escaping holder — a string[] field-read share in a returned literal

*2026-09-03* — `escaping_holder`, the row `2026-08-27-strarr-field-share-read.md`
left pinned leaking under #5338, and the enumeration of what that issue still
covers beyond the construction-retain matrix.

## The shape

```fern
function make(i: i32): P {
    var q: P = P { f: mkv(i), n: i };
    var p: P = P { f: q.f, n: i };      // f: string[]
    return p;
}
function round(i: i32): i32 { var p: P = make(i); … churn … read p.f back … }
```

3000 allocs / 2200 frees, 43 200 live over 200 rounds, against native's
3000/3000 — four blocks a round: the returned box, its buffer, two elements.

## The block was in the caller, not in the share

The pin was written on the assumption that an escaping holder was "a different
admission question" for the share. Measured against the bind spelling that
`2026-08-27-strarr-field-bind-share.md` found balancing on the same shape, it
was not the share at all. The emit for `make` is the same in both spellings:
`__struct_drop_P` for the source, the buffer left at rc 1, the target returned.
The difference is in `round`:

| spelling | `round` emits |
|---|---|
| bind (`var tt = q.f; P { f: tt }`) | `__field_reclaim_P`, `__struct_drop_P` ×3 (one per return path) |
| inline (`P { f: q.f }`) | **neither** |

`var p: P = make(i)` earns its reclaim through `return_fresh_struct_ret_fns`,
and `make` joins that registry only when `return_value_is_strictfresh_struct`
admits the returned literal. Its array-field arm admits a literal, a producer
call, a frame-built local, or any bare ident when the literal's type routes
field reclaim, on the argument that the construction retained it. `tt` is that
ident. `q.f` reached the same arm as an `ExprFieldAccess` and fell through
every case, so `make` was refused as a producer and the caller's binding was
never credited. The classifier was stricter than the lowering: the lowering had
been retaining the read since #7624.

## Why the read is strict-fresh on the ident's terms

The retain is `enum_arr_field_share_read`'s arm in the ExprStructLit lowering
(its name is the enum-array case it was written for; it fires for every
non-leaksafe, non-struct-array field type, which is `string[]`). So the returned
box carries a counted reference of its own. The source's drop releases only the
source's count; the caller's deep drop decs the literal's. `__fern_str_arr_free`
walks elements only at rc 1, so every holder that outlives the frame keeps the
count above 1 and costs a leak, never a free:

- a **parameter** source keeps its own count in the caller;
- a **second literal** built from the same read takes a third count and
  releases it at its own drop (balances — the last owner walks);
- a source that **escapes** is never dropped, so the buffer never reaches 1.

The holder must be a parameter or a local declared exactly once by annotation
or by a no-base literal; a name both a parameter and a local, or declared
twice, is refused, because every scan here is name-keyed
(`strarr_share_read_holder_type`). Restricted to `string[]` — the one field
type whose read the share admission and the retain both cover.

## Measured

Native x86-64, `bin/fern -interp` and the self-host agree on every exit;
every row re-run under `FERN_SANITIZE=1` with `FERN_RC_UNDERFLOW_TRAP=1` and
`FERN_RC_FREE_DEBUG=1`: no trap, no quarantine hit, no underflow.

| probe | native | self-host before | after |
|---|---|---|---|
| `escaping_holder` — the row | 8, 3000/3000 | 8, **3000/2200** | 8, **3000/3000** |
| bind spelling (control) | 8, 2800/2800 | 8, 3000/3000 | unchanged |
| `escaping_holder_read_twice` — two literals, one returned | 8, 3200/3200 | 8, 3200/2200 | 8, **3200/3200** |
| `escaping_holder_source_returned` — `return q` on one path | 8, 3000/3000 | 8, 3000/2000 | unchanged, leaking |
| `escaping_holder_param_source` — `make(q: P)`, caller reads `q.f` after | 76, 3000/3000 | 76, 3000/2200 | unchanged, leaking |
| `escaping_holder_shadowed_holder` — local `q` shadows param `q` | 76, 4000/4000 | 76, 4000/3200 | unchanged, refused |
| `escaping_holder_element_bound` — `var e = q.f[0]` before the share | 8, 3000/3000 | 8, 3000/2200 | unchanged, refused |

`param_source` leaks the CALLER's `q`, not the returned holder: `make(q, i)` is
a non-borrowable argument (the callee stores a field of it), so `q` earns no
reclaim in `round` — the counted-param question, not this one. `source_returned`
leaks because both holders escape on some path and the escape gate is
flow-insensitive, so neither is dropped inside `make`; the buffer never reaches
rc 1 anywhere. Both are the leak direction.

The flipped row was checked against the parent commit's `irlower.fern`: it
fails there with `must balance at live_bytes 0`.

## The #4355 exclusion list, re-measured

#5338's charter is "lift the escaping-read string-reclaim exclusions", and the
parent's tally named the excluded positions rather than sites. Each was
probed as a five-line program against native x86-64 and the self-host under
`FERN_LEAKCHECK=1`, all 200 rounds, all three engines agreeing on the exit.

**`string` fields** (`strfld_collect_unsafe`, the whole-program read scan that
gates `__field_reclaim_<T>`'s string arm, on `struct S { name: string, n: i32 }`
threaded through `s = step(s)`):

| position | native | self-host | verdict |
|---|---|---|---|
| baseline, no read | 1600/1600 | 1600/1600 | clean |
| `return x.name` from a callee | 1600/1600 | 1600/1000 | **still excluded** — the read is an uncounted alias handed out of the frame; lifting it wants a return-side retain paired with a release at the caller's binding |
| `keep(s.name)`, non-borrowable | 1600/1600 | 1600/1000 | **still excluded** — same alias, through a call that stores it |
| `t = s.name` reassign alias | 2000/2000 | 2000/1200 | **still excluded** — the assign does not retain; the bind does |
| `var t = s.name` bind, then rebind `s` | 2000/2000 | 2000/1800 | **admitted and leaking one block a round**: the #4768 read-side retain fires and `t` is never released — the bind is counted but no credit family sweeps it. Tractable: a "retained field-read" string credit under the two ordinary gates (non-escape, not reassigned) |
| `[s.name]` array element | 2200/2200 | 2200/2000 | leaking, sound — the element store is a move, and the walk does not enter `ExprArray` |
| `(s.name, i)` tuple element | 2200/2200 | 2200/1800 | leaking, sound — same |
| lambda capturing `s.name` | **2400/1200** | 2000/2000 | self-host clean; **native leaks** 67 200 bytes over 200 rounds — a native closure-capture leak, not this issue's |

**`string[]` fields** (`strarrfld_scan`, on `struct P { f: string[] }`):

| position | native | self-host | verdict |
|---|---|---|---|
| `var e = q.f[0]` element bind | **2800/2600** | 2800/2200 | refused, sound — and native leaks 48 bytes a round on the same shape |
| `for s in q.f` | 2800/2800 | 2800/2200 | **refused, tractable**: the bind spelling (`for s in tt`) is admitted when the binder is transient (`body_unsafe_for` on the binder); the direct field read is walked as a read before the loop is considered. Admitting it needs the binder-transient test at the `StmtFor` arm plus a refusal when the holder is assigned inside the body |
| `keep(q.f)`, storing callee | 2800/2800 | 2800/2200 | refused, sound |
| `peek(q.f)`, read-only callee | 2800/2800 | 2800/2200 | **refused, tractable**: the scan marks every call argument because the borrowable registry is not visible when it runs; the `string` walk parks a `?callee#idx:field` record and settles it against the registry afterwards (`strfld_defer_arg` / `strfld_resolve_deferred`), and the same two steps fit here. The open question before doing it is whether `borrowable_params_of` refuses `return xs[0]` — an element escape, which a buffer-level borrow verdict may not see |
| `return x.f` from a non-forwarder | 2800/2800 | 2800/2200 | refused, sound (#7417 admits the all-paths forwarder only) |
| `q.f.append(w("c"))` receiver | 3200/3200 | 3400/2600 | refused, sound |
| bind then store inside `if` | 2900/2900 | 2900/2300 | refused, conservative (#7644 collects flat stores only) |
| bind, store, transient `tt[0].len()` | 3000/3000 | 3000/3000 | clean |

Fixed since the tally and not re-listed: `string[][]` (#4778), `?`-reached
payloads (#4766), enum-payload struct strings (#4771/#4772), literal and fresh
temp args (#4773/#4774), the native pair-form scrutinee (#5339), and the whole
construction-retain matrix (#7624, #7644). Map string K/V is #4353's; the wasm
pair-form `?` payload is the construction-side discipline noted on #4766.

## Gates

- `TestSelfHostStrArrFieldShareReadX86_64` (9 rows, one flipped, five new) and
  `TestSelfHostStrArrFieldBindShareX86_64` — green
- the complexity ratchet (`internal/lint`) — unmoved
- the shape-selected set (TEST-GATES rule 13: files declaring a `string[]`
  struct field and pinning `frees`) — 24 files, 43 test functions
- per-module emit-all fixpoint (`TestSelfHostPerModuleEmitAllFixpointX86_64`)

## What remains under #5338

The lifting itself: the four `string`-field positions above are the
escaping-read exclusions the issue was opened for, and the parent's diagnosis
still holds for three of them — a read handed out of the frame is an uncounted
alias until something retains it at the read and releases it at the consumer.
The fourth (`var t = s.name` never released) is the retain half done without
the release half. On the `string[]` side two refusals are widenings of walks
that already exist, named above.
