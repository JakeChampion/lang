# The self-host's match over read_chunk / read_line owns the payload, and its read_chunk stops stranding the block (#8402)

*2026-09-07* — `examples/self_host/irlower.fern` (the binding half) and
`examples/self_host/asmcore.fern` (the runtime half), both register backends.
The self-host port of native's #8396 / #8399.

## The measurement

`b_pass.fern` — the pass-through loop from #8396, `match (r.read_chunk(131072))`
writing each chunk to stdout — built with `FERN_LEAKCHECK=1` by
`bin/fern-selfhost -target x86-64-linux`, 20 MB of input, byte-identical output
throughout:

| | allocs | frees | live_bytes | wall (pipe) |
| --- | --- | --- | --- | --- |
| self-host, before | 461 | **0** | **20,201,064** | 0.039 s |
| self-host, binding half only | 461 | 154 | 197,368 | 0.040 s |
| **self-host, both halves** | 462 | 155 | **12,280** | **0.015 s** |
| native, same commit | 464 | 155 | 7,440 | 0.013 s |
| `cat` | — | — | — | 0.006 s |

From a PIPE, where every read is short (64 KiB against a 128 KiB request), the
binding half alone reads **20,263,624** live bytes — the whole input again — and
both halves together read 24,520 against native's 14,784. That gap is the
runtime half, and measuring only from a file hides it completely.

What is left on either input is the per-call IMMORTAL Option / Result box, a
constant (79 B a call here against native's 48): the self-host twin of #8405,
untouched by this.

## The binding half

`lower_stmt_match` bound `Ok(chunk)` like any other enum payload — `mark_str` on
a slot read straight out of a box nothing sweeps, so the string the helper built
for this caller had no owner at all. Native's answer is the `consumingBindings`
role; the self-host's equivalent role is the `"STR:"` reclaim credit, and the
port is three pieces:

- `owned_payload_builtin_ptype` names the callee spellings whose box is immortal
  and whose success payload is fresh — `Reader.read_chunk`, `Reader.read_line`,
  `read_line`, `env`, `read_file` (string) and `read_file_bytes` (u8[]) — the
  mirror of native's `rcOwnedPayloadBuiltins`, read off `asmcore`'s own runtime
  bodies. `fn_names` keeps a user declaration of the same name out.
- the bind site stores through `emit_str_reclaim_store` / `emit_arr_store`, so
  each iteration releases the previous iteration's payload. Without it a loop
  strands every chunk but the last.
- the binding takes the `"STR:"` credit keyed on the ARM's site, so the exit
  sweep releases the last one; an owned ARRAY binding simply skips the
  `borrowed_arr` opt-out and rides the ordinary is_arr sweep. `Ok(_)` at an owned
  position drops the payload on the spot.

Admission is withheld — today's leak, never a transfer — for a guarded arm, an
alternation continuation, a sub-pattern, and any binding `binding_escapes_arm`
refuses. That last one is passed the frame's REAL borrowability registry rather
than the empty one the option-payload family uses, which is what admits
`w.write(chunk)`: `Writer.write` is a copying builtin (`copying_builtin_keys`),
so the pass-through loop qualifies. With the empty registry it does not, and the
headline measurement above stays at zero frees.

## The runtime half

`__fern_reader_read_chunk` was `__raw_alloc(n)` … `Ok(__raw_string(p, r))`. The
question #8402 said to check before assuming: `__raw_string` over a `__raw_alloc`
pointer is the FUSED form, and `__fern_str_free`'s fused arm computes the
freelist class from `3 + ceil(len/8)` words with **len the SHORT length** — so a
short read hands the freelist a block bigger than the class it lands in and the
next `__raw_alloc(n)` finds nothing to recycle. asm_ir.fern's own comment on
that arm already said so ("a producer that over-allocated … wastes the slack").
The `r < 0` path lost the block outright.

There is no raw free intrinsic, and none was added: a fused string over the block
IS the language's way of naming its owner, so the helper binds `full =
__raw_string(p, n)` — the whole block, at its true size — and lets the frame's
own reclaim give it back. A short read is copied into an exact-size block with
`__memcpy`; EOF answers the `.rodata` empty literal; the error path returns with
`full` still dead. `__fern_read_line` takes the same shape: its 256-byte buffer
was boxed at the line length (stranding the rest) and lost outright at EOF.

Verified before writing it: a dead `var full: string = __raw_string(p, n)` is
credited `"STR:"` and swept — 200 rounds, `allocs=200 frees=200 live_bytes=0` —
and so is the copy shape with the early return, so the helper degrades to today's
leak if the credit is ever withdrawn rather than to a dangle.

## What gates it

`conformance/cases/alloc_flat_read_chunk` — the case #8396 landed against #8402
in the self-host known-divergence ledgers. Both rows are gone; the x86_64 leg
fails now if it comes back. `TestSelfHostOwnedPayloadReclaimX86_64` /
`…ShortReadReclaimX86_64` / `…HazardsX86_64` pin the three shapes directly.

## Traps

- **Measure from a PIPE as well as a file.** A file gives full-size reads, so the
  short-read strand is invisible: 12,280 live bytes from a file and 20,263,624
  from a pipe, same binary, before the runtime half.
- **`binding_escapes_arm`'s registry argument decides the headline case.** Every
  existing caller passes the empty registry, where a call argument is
  conservatively a retain. The reader loop's whole body is `w.write(chunk)`.
- **A per-call constant hides in a doubling test**, same as native: the immortal
  boxes cost ~79 B a call, so the probes compare the same call count at a 64x
  payload size.
- The guard keyword is `when`, not `if`; an `if` guard is a compile error the
  driver reports as exit 1 with no diagnostic through the test harness.

## Still open

- The immortal Option / Result boxes (the self-host's #8405): ~79 B a call, the
  whole of the 12,280 above.
- `__fern_read_all_stdin` allocates 32 MiB and boxes it at the total read, so it
  strands the same way. Left alone deliberately: copying a whole input to fix it
  doubles peak memory, which is a design call rather than a bug fix.
- The `?` binding form (`var s: string = r.read_chunk(n)?`) is a different site
  (`try_box_fresh`) with its own credit, `collect_try_str_binding_names`, keyed on
  the OPTFRESH registry of user producers. These builtins are not in it, so that
  shape keeps today's leak; the match form is what #8402 measured and what this
  closes.
- No rc-corpus row was banked for this family. `rcCorpus`'s leak leg pins an
  exact byte count per backend and the shape cannot reach zero while the box is
  immortal (8 EOF reads measure 288 bytes on native x86-64); a non-zero row would
  also have to bank an arm64 and a wasm number, and the wasm one is not
  measurable here. The self-host probes assert the constant-vs-scales SHAPE
  instead, which is what the conformance case asserts and does not go stale when
  #8405 lands.
