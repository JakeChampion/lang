# 2026-09-06 — a threaded struct local survives a tuple handback (#8678, #8679)

`coreutils/seq` threads its output block through `drain`, which returns it
inside a tuple:

```
out = push_str(out, sep);
out = emit_term(w, out, f, x);
var d: (io_buffered.BufWriter, Block) = drain(w, out);
w = d.0;
out = d.1;
```

Under the self-host every generation of `out` leaked — the superseded box and
the 64 KiB `.with` copy of its buffer, 131 KB per printed line — until the OOM
killer took `seq 1e29 1000 1e29` at 88,638 of its 4,294,968 lines. The native
build finishes in 36 s at 1.1 GB peak.

## What refused the credit

`collect_snap_locals` admitted `out` (an alias of the param, later a
call-derived local) on every predicate but `body_consume_unsafe_for`, and the
instrumented verdict named four statements, one at a time:

1. `return (w, out)`. The return arm admitted a bare `return name`, a struct
   literal carrying it, and `return g(.., name, ..)`, and read a TUPLE literal
   as a container escape. The same reading left `flush`'s `return (o, b)`
   marking its parameter non-consume-safe, so every caller handing a threaded
   local to `flush` or `drain` was refused too. `ret_hands_back` now judges a
   tuple element by element under the rules the other three shapes already
   had.
2. The handback pair `var d = g(.., out, ..); out = d.f;` was recognised only
   when the unpack was the very next statement. `handback_unpack_at` scans
   forward over statements that mention neither name nor `d`, or that read a
   DIFFERENT element of `d` whose declared type is not the handed-back
   element's — `w = d.0` — and refuses a second read of the same element.
   `dor_scan` (the `NOFLD:` box-only decision) takes the same pair.
3. `write_overflow(o, out)` in `emit_term`'s `None` arm: a call in statement
   position, result discarded. `expr_consume_transparent` admits it when
   every mention of name sits at a consume-safe position of some call, or is
   a projection of such a call passed on only at consume-safe positions —
   `finish(flush_block(o, b).0)` inside `write_overflow`. A scalar-typed
   position is consume-safe by construction (no box arrives there), which
   the registry now says outright; without it `finish(w: i32)`'s `w % 100`
   read as an escape of `w`.
4. `drain`'s slow path `return flush(w, b)`. `tuple_fresh_ret_fns_of` wanted
   a direct literal on every path, so `var d = drain(w, out)` earned no
   "TUP:" box credit and the 40-byte tuple box leaked per iteration. The
   registry is a least fixpoint that also admits a return that is a call to
   a member; a function admitted only that way records no `ARRF:` flags.

## The count nobody gave back

With the credit admitted, `drain`'s identity path `return (w, b)` still leaked
the whole generation: the tuple literal retained the bare parameter, so the
caller received its own box at rc 2, `out = d.1` found old == new and did
nothing, and the next rebind found the box shared and took the box-only arm —
the buffer went with the dead tuple. A returned borrowed struct param is an
UNCOUNTED alias by the Return lowering's own rule (#8240); `lower_expr_tuple`
now skips the retain for a bare parameter element when the literal is the
function's return value. In binding position the retain stays: there the
`t` element kind's exit dec balances it.

## Measured

Self-host x86-64, `FERN_LEAKCHECK=1`, unreclaimed blocks (allocs − frees):

| shape | before | after |
|---|---|---|
| `return (w, out)` with a rebinding callee, 2000 rounds | 4,004 | 4 |
| handback with `w = d.0` between, 2000 rounds | 4,008 | 4 |
| identity handback every round (`drain`), 2000 rounds | 5,990 | 4 |
| `emit_term` shape (void call in a match arm), 2000 rounds | 13,020 | 7 |
| `seq 1 1 10000` (#8679's row) | 10,156 / 1.31 GB | 10,156 / 836 KB |
| `seq 1e29 1e26 2.2e29` (1201 terms, the ld path) | 114,179 / 162 MB | 109,370 / 4.8 MB |

The residue in the reductions is the caller's own final block. `seq`'s block
family is closed on both paths; what remains there is the second class the
issue named — `core/bigint` and `lib/ld` temporaries in `print_numbers`'
per-term arithmetic (≈90 blocks, 4 KB per term; native frees all but 290 B),
which is the struct-returning call-chain reclaim and its own issue, #8723. At
4 KB a line the parity row still exceeds the box, so
`TestSelfHostCoreutilsParity/seq/ten_to_the_twenty_nine_by_a_thousand` stays
red for that class alone.

## Witnessed

`internal/e2eselfhost/self_host_tuple_handback_reclaim_test.go` — five
shapes at 200 and 2000 rounds on x86-64, arm64 and wasm; the unreclaimed
count may not move with the loop. A sanitizer leg on x86-64 pins the other
direction: the rebind release now runs where it was refused, so the quarantine
and the over-release trap must stay silent.

## Traps

- The `emit_term` shape reduced clean twice before it reproduced: the leak
  needs the discarded call's argument to reach the callee as a projection of a
  nested tuple-returning call, and a scalar-typed parameter at the outer
  position. Reduce with the exact nesting.
- `var_annot_at_top` on the inner block reads a function-level local as
  untyped; the handback compares the two ELEMENT types of `d`'s own annotation
  instead, which needs no lookup of name's type at all.
