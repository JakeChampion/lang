# Experiment 2: the bounded packet codec

What #9584 asked, measured. The programs are `examples/fip/packet_*.fern` —
four implementations of one binary protocol, differing only in where the bytes
live — and `internal/e2e/fip_packet_test.go` keeps their claims from rotting.

Read `docs/ALLOCATION-OBSERVABLE.md` first for what the allocation numbers mean,
and `docs/FIP-EVENT-LOOP.md` for experiment 1. This one exists because that one
could not see the difference it was looking for: its workload wrote **one
element per event**, so `fip` and `fbip` measured the same and copying never
appeared. A packet codec writes up to 240 bytes per request, and both gaps open.

## The protocol

A 16-byte fixed header and a bounded payload, so a whole packet fits in 256
bytes and every buffer can be sized once, up front.

```
0..1   magic 0xFE 0x52        6..9   request id, u32 little-endian
2      version                10..11 payload length, u16 little-endian
3      message type           12..13 Fletcher-16 of the payload
4      status (0 on request)  14..15 reserved
5      reserved
```

Four message types, chosen so the copying differs between them: `echo` and
`reverse` move the whole payload, `upper` moves it and rewrites some of it,
`sum` reduces it to eight bytes. Six rejections: bad magic, bad version, unknown
type, oversize length, truncated frame, checksum mismatch.

## The program

256,000 requests, 64 per round, generated from a mixing function so payload
lengths sweep 0..240 and all four types appear. **Every 17th request is
malformed**, cycling through the six rejections, so the validation ladder is
exercised by the thing being measured rather than by a separate test that proves
nothing about throughput.

| variant | where the bytes live | annotation |
| --- | --- | --- |
| `packet_baseline.fern` | a record holding its own copy of the payload | none |
| `packet_borrowed.fern` | offsets into the wire buffer | none |
| `packet_fbip.fern` | one `Codec` struct of buffers and counters | `fbip` |
| `packet_fip.fern` | one owned `u8[]`, threaded | `fip` |

`packet_fip.fern` runs in two modes: framing the response **over** the request
in one buffer (the default), and into a **separate** preallocated output buffer
(`separate` on the command line).

All five produce byte-identical responses: 240,941 served, 2,510 of each
rejection bar 2,509 bad checksums, and one digest — a running hash over every
response byte — of 371630525. That agreement is the experiment's differential
test; a variant that framed a different answer is caught rather than reporting a
throughput win for doing less work.

## What it measured

x86-64 Linux, 4-core container, medians of five runs. "Round" latency is one
batch of 64 requests end to end; run-to-run spread was under 3%. The workload is
sized to match experiment 1's programs, because the SSA differential sweep over
`examples/` runs each of these under both backends under qemu — five times the
requests cost five times the lane time and told us nothing more.

| variant | allocs/req | bytes copied/req | ns/req | req/s | p50 | p99.9 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `baseline` | 20.536 | 302 | 9177 | 108,957 | 584,119 | 815,028 |
| `borrowed` | 13.464 | 189 | 8048 | 124,248 | 511,523 | 686,730 |
| `fbip` | 0.000 | 102 | 7590 | 131,742 | 481,286 | 679,187 |
| `fip` | 0.000 | 74 | 6206 | 161,114 | 394,141 | 540,374 |
| `fip-separate` | 0.000 | 102 | 6392 | 156,428 | 405,376 | 574,985 |

Latencies in nanoseconds per 64-request round.

### Here `fip` and `fbip` do NOT measure the same

Experiment 1's headline was that the two annotations were indistinguishable —
362 vs 398 ns, inside the run-to-run noise. That result was about its workload,
not about the annotations: one element written per event is one struct
reconstruction either way.

Write 240 bytes instead and the gap is **1.22x** (7590 vs 6206 ns per request),
well outside a 3% spread. Both are flat at zero allocations. The difference is
that a byte written into a struct-held buffer is a whole `Codec { ...c, out:
c.out.with(i, v) }` reconstruction, while the `fip` plane threads the buffer as
its own `own` parameter and writes the store. Isolated, with both doing the
same work and both allocating nothing, that is 12.2 ns per byte against 4.0 —
a factor of 3.0, stable across runs. End to end it is 1.23x because the rest of
a request is the same in both.

So the choice between them is not free after all, and what decides it is
**granularity**: how many elements the operation touches per consumption of the
state.

### Allocation-free is not copy-free, and only one of them is visible

The four variants' allocation counts are 20.5, 13.5, 0 and 0 — which says the
ladder has two rungs. The bytes-copied column says it has four: 302, 189, 102,
74. Between `fbip` and in-place `fip` the allocation counter reads identically
zero while a quarter of the remaining copying disappears.

That is the whole reason #9584 asked for bytes copied as well as allocations.
An allocation-free program still moves memory, and past a certain point moving
it is the cost.

### The in-place echo moves nothing

`fip` in place copies 74 bytes per request against `fip-separate`'s 102, and the
two differ in one respect: when the response is framed over the request, an
`echo`'s answer is **already in the buffer**. The whole response is 16 header
bytes rewritten and no payload movement at all.

No other variant here can express that. It is worth 3.0% of throughput on this
workload (161,114 vs 156,428 req/s) — small, because echo is a quarter of the
traffic and the payload averages 120 bytes — and it is the shape that scales
with payload size while the others do not.

The separate-buffer mode is not a worse version: it is what a server needs when
the request must outlive its own response. The point is that the language can
express both and the checker verifies both, and the cost of the choice is
visible here rather than guessed at.

### Borrowing gets a third of the way

The sliced parse removes the payload copy and nothing else: 302 → 189 bytes, 20.5
→ 13.5 allocations, 109k → 124k req/s. It is the change a reader reaches for
first and it is worth having.

What it does not remove is the two allocations it never mentions — the
transform's fresh output array and the encode's fresh response frame — which are
most of what is left. "Just take a slice" is a third of the distance to the
disciplined variants, and the remaining two thirds need somewhere for the output
to live, which is what an owned buffer is.

## What the language forced

Four things, in the order they were hit. Two are fixed, one is filed, one is a
design fact worth writing down.

### The self-host could not check the `fbip` variant at all (#9699 — FIXED)

The `.with` receiver root walk reached an identifier in native and only a *bare*
identifier in the self-host, so `own c: Codec` … `c.out.with(i, v)` — the whole
structure-of-buffers shape — was clean natively and E053 self-host.
`examples/fip/event_loop_fbip.fern`, from experiment 1, did not compile under
the self-host compiler either. Fixed by spelling one rule in both.

Behind it sits #9700: the self-host's E068 does not pair an `fbip` struct-update
spread with the `own` it consumes, so experiment 1's variant still does not build
self-host. Both experiments' `fbip` variants are native-only until that lands.

### The readable spelling of an encoder allocates (#9702 — FILED)

A 16-byte header is a run of byte stores, and the spelling that says so is a
chain:

```fern
out = out.with(0, magic0() as u8).with(1, magic1() as u8).with(2, version() as u8);
```

In **assignment** position that allocates one fresh box per evaluation — 10,001
allocations over 10,000 calls, against 1 for the same writes as separate
statements. `packet_fip.fern` therefore spells every store on its own line, and
the first draft of it reported 15 allocations per request while claiming `fip`.

This nearly went the other way. #9699 originally widened the checker to admit the
chain, reasoning that the second link holds the unique array the first returned —
right about uniqueness, silent about the lowering. A checker test cannot see a
runtime allocation, so it was green. The measurement above is why it was
withdrawn.

### A parse that returns a record allocates, per request

`Request { status, msg_type, req_id, at, len }` is five scalars and still a heap
box, with no donor to pair it with. The baseline and borrowed variants pay it;
the disciplined ones cannot, so `packet_fbip.fern` folds those five into its
state and `packet_fip.fern` keeps them as locals in the one function that uses
them.

This is the first place the discipline changes the *shape* of the code rather
than its annotations, and it is not obviously worse: "the parse writes its
findings into the connection" is how a C codec would be written anyway.

### An owned buffer cannot be its own borrowed source

An in-place transform and a copying one cannot share a body. `sum_into(b, b, …)`
— handing the owned buffer in as its own `src` — is E050, correctly: the callee
would write through one reference while reading through another. So
`packet_fip.fern` carries `sum_inplace` beside `sum_into`, `rev_inplace` beside
`rev_into`, and so on.

Two spellings of four transforms is not a large tax here, but it grows with the
number of operations, and it is the reason the in-place mode is a *mode* rather
than a wrapper.

### Reading state in the expression that consumes it

`c = put(c, i, wire[c.at + i])` is E050: the argument is evaluated after `c` has
moved. Every loop in `packet_fbip.fern` hoists its field reads first. The
diagnostic is right and the fix is mechanical, but it is a second thing to know
before an `fbip` loop compiles, after the reconstruction-per-element shape.

## Answers to the questions #9584 asked

- **Strict hot path FIP where feasible** — yes, the whole data plane, both
  modes. The driver around it is ordinary Fern: it owns the buffers, tallies and
  prints. The plane returns one owned buffer and nothing else, which is why the
  status and response length come back in the response header, where a caller
  reads them anyway.
- **Zero steady-state allocations** — `fbip` and both `fip` modes, over 256,000
  requests. The two controls allocate 20.5 and 13.5 per request, which is what
  makes the zero mean something.
- **Malformed-input tests** — in the steady state, not beside it. Every 17th
  request is corrupt and all six rejections fire ~2,510 times each.
- **Throughput/latency comparison** — the table above, medians of five, with
  p50 and p99.9 per round.
- **Bytes copied per request** — measured, not estimated: each variant counts
  the bytes its transform and encode actually write. 302 / 189 / 102 / 74.

## What this says about the architecture

Experiment 1 said the discipline is worth about 2.5x and the choice of
annotation is worth nothing. Experiment 2 says the second half of that was a
property of the workload. On byte-granular work the annotations differ by 1.22x,
and the reason is mechanical enough to predict: `fbip`'s guarantee is per
*construction*, so its cost scales with how many constructions the operation
needs, and holding a buffer in a struct makes that one per element.

The rule this suggests: hold state in a struct when operations touch a bounded
number of its fields, and thread it as `own` parameters when they sweep it. The
event loop is the first, a codec is the second, and the language checks both.
