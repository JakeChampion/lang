# The self-host's `print` stops building a temp and its `Writer.write` gives the box back (#8410)

*2026-09-07* — `examples/self_host/irlower.fern`, both register backends and
wasm. Two builtins, two independent causes, one measurement: each left exactly
one block per call **with a string literal argument too**, so the copying-builtin
credit (#8394) was not what was missing.

## The measurement

`bin/fern-selfhost -target x86-64-linux`, `FERN_RC_TRACE=1`, three calls each,
paired by pointer:

| call | before | after |
| --- | --- | --- |
| `print("abcdefghabcdefghabcdefgh")` | 3 allocs / 0 frees, **56 B a call** | **0 / 0** |
| `print("ab")` | 3 / 0, **32 B a call** | **0 / 0** |
| `w.write("abcdefghabcdefghabcdefgh")` as a match scrutinee | 3 / 0, **40 B a call** | **3 / 3** |
| `w.write("ab")` as a match scrutinee | 3 / 0, **40 B a call** | **3 / 3** |
| 200 × `w.write("x")` in a loop | 200 / 0 | **200 / 200** |
| `eprint(…)`, `__memchr(…)`, `__count_byte(…)` | balanced | unchanged |

## `print`: the temp was never needed

`print(s)` lowered to `print_str(s + "\n")` — one `str_concat`, one write(2) —
and the joined box, whose size tracked the argument, was released by nobody. The
sibling next to it was already right: `eprint` writes the payload out of the
caller's own box and then one newline byte out of the shared static scratch
(`asmcore.rt_src_eprint_str`), so it allocates nothing at all. Native's
`__fern_puts` is the same two writes.

So the fix is a deletion: `print` is now `print_str(payload)`, `drop`,
`print_str("\n")`. Both payloads are boxes somebody already holds — the second
is a `.rodata` literal — so the pair allocates nothing, and it needs no new op,
no new runtime helper and no backend case. `write(s)` was and stays a bare
`print_str`.

The remaining `print(a + "def")` leak is the ARGUMENT's own temp, and `eprint`
has it identically (3 blocks for 3 calls, both, after this) — a fresh string
built at a call argument and released by nobody. That is a different shape from
this one and is not `print`'s.

## `Writer.write`: the box had an owner all along

Unlike native, where `__fern_writer_write` allocates a header-less box that
`rcresults.go` classes immortal (#8398 / #8405), the self-host's helper is
ordinary Fern source (`asmcore.rt_src_writer_write`): its `Some(io_error)` /
`None` build an ordinary rc=1 box through the same `opt_make` / `opt_none` every
user `Option` uses. Nothing was immortal. The box simply had no release, because
`lower_stmt_match` emitted one only for a `map.get` scrutinee.

`match_scrut_owns_fresh_result_box` is that classification with `Writer.write`
in it, gated by `resource_method_opt_ret_type` — the same receiver gate
`lower_call_method` applies, so it cannot claim a user `.write()` on a struct, an
enum, a string or an array, and cannot drift from the call that is emitted. The
release is `emit_scalar_enum_box_free`, the shallow dec + slot zero the map-get
reclaim already uses. On the `None` path — every write that succeeds — that is
the whole block. A FAILED write also builds a fresh `IoError` inside the box
(`__fern_io_error`), and that one keeps today's leak: deep-dropping it needs the
variant walk plus a proof the `Some` arm's binding does not outlive it, which is
the payload-kind gap #8806 covers. One block per FAILED write, against one per
write.

It is emitted TWICE, and the pair is disjoint by construction because the free
zeroes the slot and `__fern_rc_dec` is null-safe:

- **in the arm**, once the arm has its bindings and before its body. This is what
  covers an arm that `return`s — `match (w.write(chunk)) { Some(_) => { return
  -1; }, … }` is the idiomatic write check, and a join-only release never runs on
  that path. A guarded arm can still fall through to the next one and an
  alternation continuation shares its predecessor's block, so both decline here.
- **at the join**, which catches the wildcard arms, the guarded ones, and a
  scrutinee no arm matched.

An `@` binding aliases the whole box into a name that outlives the arm, so it
declines — `match_binds_whole_scrutinee`, now the shared precondition of both
post-match box releases rather than a loop each had its own copy of.

## What is still open here

- **The bound-only form.** `var e: Option[IoError] = w.write(s);` with no match
  still strands its box — but so does `var e: Option[IoError] = g(s);` for a USER
  `g`, and so does `match (g(s))` on one. The scalar-payload sibling
  (`Option[i32]`) is released in both forms. The gap is the PAYLOAD KIND in
  `consumed_scalar_enum_frees` / `consumed_rcpayload_option_frees`, not the
  builtin, and it is #8806.
- **The rest of the I/O result-box family.** `read_chunk` / `read_line` / `env` /
  `read_file` / `read_file_bytes` hand back the same shape of box, still released
  by nobody: the self-host's #8405, and what #8402's own "still open" list
  already names. Their payload ownership is #8402's and is unaffected either way
  — a shallow box free never touches the payload.
- **`close` and the `open_*` constructors**, deliberately left out of the
  classification rather than overlooked. Their boxes are the same shape from the
  same shape of helper, but the loop that would gate them —

  ```
  match (open_reader("/dev/null")) { Ok(r) => { match (r.close()) { … } }, Err(e) => … }
  ```

  strands **three** 40-byte boxes a round, not two, so admitting one or two of
  them cannot be told from admitting none by any measurement this file would
  accept. That family goes in together, with the read side, as #8405's self-host
  twin.

## Traps

- **Measure `print` with a LITERAL argument.** With `print(a + "def")` the
  argument temp leaks too, and the two blocks look like one story. The literal is
  what showed print's block scaling with the argument while write's stayed at 40.
- **The self-host's I/O result boxes are not native's.** The rc-log entry next to
  this one calls them immortal, which is true of native's `__fern_alloc_box`
  helpers and false here: the helper is Fern, so the box is rc=1 and a plain dec
  reclaims it. Checking that before porting native's design is what made this
  half a five-line admission rather than a runtime change.
- **A per-call leak hides in an absolute byte count.** The gate compares the same
  program at 20 and at 200 rounds and requires equal live bytes, and the same
  call count at 12× the argument for the same reason `#8402`'s probes do.

## What gates it

`TestSelfHostCopyingBuiltinArgX86_64`'s `print` and `writer_write` rows, which
pinned exactly one unpaired block per round and now pin zero — every block each
round allocates is freed. `TestSelfHostPrintWriteBoxReclaimX86_64` pins the two
independence shapes directly (round count, argument size) plus the guarded arm
and the returning arm, and checks what actually reached fd 1 so a lost newline is
a failure rather than a quieter pass.

Each piece was reverted in isolation, and each has a leg that goes red on its
own:

| reverted | what fails |
| --- | --- |
| the `print` lowering | `copying/print` (200/150, 2800 live) and `PrintWriteBox/print`: 640 live at 20 rounds against 6,400 at 200, and 6,400 narrow against 11,200 wide — the two shapes the probe names |
| `owns_box` entirely | `copying/writer_write` (200/150) and all three `writer_write*` legs, 8,000 live at 200 rounds |
| the IN-ARM release only | `writer_write_returning_arm` alone: 200 allocs, **199** frees, one 40-byte box — the round whose `None` arm returns out of the function |
| the JOIN release only | `writer_write_guarded_arm` alone: 200/0, because a guarded arm and a wildcard both decline the in-arm release by construction |

The returning-arm probe earns that row only because it leaves the function from
INSIDE the matched arm. Written the obvious way — the loop condition ends it and
the `return` sits in an arm that never fires — it passes with the in-arm release
removed, which is what a probe for this has to avoid.
