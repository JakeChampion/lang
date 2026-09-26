# Experiment 1: the bounded event loop

What #9583 asked, measured. The programs are `examples/fip/event_loop_*.fern`
— three implementations of one bounded key/value event loop, differing only in
how state is held — and `internal/e2e/fip_event_loop_test.go` keeps their
claims from rotting.

Read `docs/ALLOCATION-OBSERVABLE.md` first for what the two allocation numbers
mean. The short version: **fresh bytes** is what the allocator had to buy and
**allocations** is how many times it ran. A recycling steady state is flat in
the first and busy in the second, and this experiment is the case that makes
the difference matter.

## The program

A table of at most 256 entries, a bounded input queue of 64 events, a bounded
output ring of 64 responses, and four operations — create, update, read,
delete. Overload drops and counts the drop; nothing grows. The workload is
read-heavy with live data (60% reads, 20% updates, 10% creates, 10% deletes)
over ids drawn from a mixing hash, prefilled so the table has something in it,
and is byte-identical across the three variants.

| variant | state | annotation |
| --- | --- | --- |
| `event_loop_baseline.fern` | `Event` enum, `Entry[]` table, arrays that grow and shrink | none |
| `event_loop_fbip.fern` | one `State` struct of named parallel arrays | `fbip` |
| `event_loop_fip.fern` | one `i64[]` with hand-written region offsets | `fip` |

All three produce the same answers: 647,520 hits, 632,480 misses, 142 live
entries after 1,280,000 events. That agreement is the experiment's differential
test — three memory disciplines over one state machine — and a variant that
drifts is caught by the gate rather than reporting a speed win for doing less
work.

## What it measured

x86-64 Linux, 4-core container, medians of five runs of 20,000 rounds × 64
events. "Round" latency is one batch of 64 events end to end.

| variant | allocs/event | ns/event | events/s | p50 | p95 | p99 | p99.9 | max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| baseline | 9.485 | 923 | 1,082,466 | 56,737 | 74,523 | 99,035 | 129,536 | 161,767 |
| `fbip` | 0 | 362 | 2,759,960 | 21,519 | 33,958 | 46,938 | 83,961 | 156,320 |
| `fip` | 0 | 398 | 2,508,865 | 23,637 | 35,043 | 47,750 | 85,466 | 171,099 |

Latencies in nanoseconds per 64-event round.

### The discipline is worth about 2.5x, and the annotation is worth nothing

Removing allocation from the steady state took the loop from 1.08M to 2.5-2.8M
events/s and from 9.485 allocations per event to **zero**. That is the
experiment's headline and it is not subtle.

What is subtle is that **`fip` and `fbip` measure the same**. 362 vs 398 ns per
event, over runs that vary by more than the gap between them; there is no
performance argument for choosing one over the other here. The choice is about
what the compiler checks and how the code reads, and on that second axis they
are far apart — see below.

### Allocation-free does not mean flat

Absolute latency improves at every percentile. The SPREAD does not:

| variant | p99.9 / p50 |
| --- | ---: |
| baseline | 2.28x |
| `fbip` | 3.90x |
| `fip` | 3.62x |

The zero-allocation variants have a *worse* ratio than the allocating one,
because their p50 fell by 2.6x while their p99.9 fell by only 1.5x. What is
left in the tail is not allocation — it is the scheduler on a shared
container, and that cost is the same whether a round takes 21 microseconds or
57. So: **on this machine, removing allocation buys a big constant, not
predictability.** A quieter machine is where the predictability claim should be
tested, and that is a reason to run these on dedicated hardware before the
guide (#9594) says anything about tail-latency determinism.

### The baseline's 12.1M allocations bought no memory at all

Over the measured region the baseline allocated 12,141,311 times and moved the
bump mark by **480 bytes**. Every one of those allocations was served from the
freelist. Under `__heap_bump_bytes()` alone — the only allocation observable
Fern had before #9596 — this run and the two zero-allocation runs are
indistinguishable. That is the whole case for the allocation count, made by a
program rather than by argument.

### The same three answers on every backend

wasm (wasmtime) and arm64 (qemu) run the same programs to the same outcomes —
647,520 hits, 632,480 misses, 142 live — and to the same allocation verdict.
The counts are identical because the count is of allocator calls the program
causes, not of anything a backend chooses:

| backend | baseline allocs/event | `fbip` | `fip` | `fip` ns/event |
| --- | ---: | ---: | ---: | ---: |
| x86-64 | 9.485 | 0 | 0 | 398 |
| wasm | 9.485 | 0 | 0 | 445 |
| arm64 (qemu) | 9.485 | 0 | 0 | 1,895 |

The arm64 figure is qemu's, not the hardware's, and is here only to show the
allocation verdict holds there too. On wasm the ordering of `fip` and `fbip`
reverses (445 vs 463 ns) — another reading that the two are the same speed and
the gap is noise.

## What the language forced

### A `fip` function could not hold state in a struct (#9602 — FIXED)

**This is what the experiment found, and the reason `event_loop_fip.fern` is
written the way it is. It has since been fixed; the shape below now compiles.**

The architecture #9582 sets out is `fip iteration(own state) -> state`, and it
was not expressible over a struct:

```fern
fip function bump(own s: State): State {
    s.count = s.count + 1;                     // E048: rebuild with `T { ...old, count: v }`
    return State { ...s, count: s.count + 1 }; // E053: `fip` may not allocate (struct literal)
}
```

Each diagnostic recommended what the other forbade. So a strict `fip` data
plane owned exactly one array, and `event_loop_fip.fern` therefore packs a
queue, an output ring, three table regions and eight header words into a single
`i64[]` behind hand-written base offsets. It works, it is fast, and nothing
about it is checked: a wrong base constant reads the neighbouring region and the
compiler cannot know.

The fix admits the constructor SHAPE in every tier and leaves the verdict to
E068 at the IR, which counts the sites that lowered to a real allocation rather
than to a reuse-paired one — so the rebuild above compiles and allocates
nothing, while an un-paired construction is still refused. The packed-array
variant is kept as measured rather than rewritten: it is the record of what a
single owned array costs, and rewriting it would throw away the comparison this
document reports.

One limit is unchanged: a `fip` function still cannot return several owned
containers, because a tuple literal has no donor to pair against.

`fbip` never had this trouble — `State { ...s, keys: s.keys.with(i, v) }` is
just a struct update — which is why the `fbip` variant is the one a person
would want to maintain.

### A `fip` call graph must be `fip` all the way down

`fip` may only call `fip` (E053), and `fbip` may only call `fip` or `fbip`. Both
may also call the builtins that allocate nothing (`fipNonAllocBuiltins`: the
byte-scan kernels, the bit counts, `__ptr_width`, the heap counters and
`monotonic_ns`). In practice this is fine: read-only helpers take their arrays borrowed and carry
the annotation, and the capacity constants are `fip` functions. It is worth
knowing before starting, because it means a data plane cannot borrow one
formatting helper from a library that is not annotated.

### Threading `own` state through an intermediate local (#9541 — FIXED)

An intermediate local handed over at its last use used to be E051:

```fern
var t: i64[] = s.with(0, 1i64);
return bump(t);                  // was E051; a move now
```

so every step had to write back to the owned parameter itself
(`s = s.with(0, 1i64); return bump(s);`). E051 now admits a local at the
position where it dies, by the same analysis the IR moves it by
(`checker.CallArgDeaths`), and both spellings compile. A local still read after
the call, or handed over inside a loop, stays E051.

### Reading an array field inside the update that rewrites it cost a full copy (#9605 — FIXED)

**This is what the experiment found, and why the `fbip` variant hoists its
reads. It has since been fixed; both spellings below now allocate nothing.**

The `fbip` variant's first measurement was 127,073 allocations over 1.28M
events — about one per ten — and every one was a whole-array copy:

```fern
// 1 allocation per update: the read retains the field, so `.with` finds it
// shared and copies 2 KB instead of writing one slot.
s = State { ...s, vals: s.vals.with(at, s.vals[at] + delta), ... };

// 0 allocations: the same computation, read hoisted one line.
var current: i64 = s.vals[at];
s = State { ...s, vals: s.vals.with(at, current + delta), ... };
```

Read-modify-write is what an update IS, so this is not an exotic shape, and
nothing reported it: the copies are recycled, so fresh bytes stay flat and the
leak census balances. It took the allocation count to see it at all. The
single-array `fip` form did not have the problem — reading through a directly
owned array is not a retain — which was the evidence that it was field access
specifically.

The fix was not a new rule but an existing one, scoped past this case twice
over. A read sitting inside the call's own index / value arguments has already
produced its scalar when the store runs, because `emitArraySet` lowers both
arguments before it loads the receiver — and that excusal existed for a
bare-ident receiver, while the field path only excused a read in an EARLIER
statement, which is precisely the hoisted spelling. The inline read fell
between them. The hoist in `event_loop_fbip.fern` is kept, with its comment
rewritten: it is the shape the measurement above was taken on.

## Answers to the questions #9583 asked

**Can the whole processing call graph be FIP?** Yes, and the checker requires
it: E053 refuses a non-`fip` callee outright. The `fip` variant is 41 `fip`
functions and two plain ones — `main` and the `new_state` that builds the
arena, which are the control plane and are meant to be outside it.

**Is threading `own` state ergonomic?** No. The owned value must be written
back to the parameter at every step, an intermediate local is rejected with a
diagnostic that does not name the remedy, and with a struct forbidden the state
has to be packed by hand into one array. The `fbip` spelling is ergonomic and
measures the same.

**Which structures prevent FIP?** As measured: structs, tuples and any
multi-value return, enums with payloads — every one of them is a constructor,
and a constructor was an allocation E053 refused. #9602 has since admitted the
constructor shape wherever a donor can be paired against it, so structs and
payload-carrying enums are available; a tuple return still is not, having no
donor. What is left is arrays of scalars,
threaded one at a time. Closures too: capturing the state to pass it to a
callback shares it, so the harness's own `bench.run(…, () => …)` could not be
used to drive the `fip` variant, and the drivers sample the clock inline
instead.

**Can overload be handled without allocation?** Yes, and it is the easy part. A
full queue increments a counter and drops the event; the capacity is a
constant; nothing on the overload path constructs anything. All three variants
do this identically, and the measured runs report `dropped: 0` because the
workload fits — the path is exercised by the prefill, which overflows the
64-slot output ring by design.

## Acceptance, against #9583

- Explicit capacities: yes, three constants at the top of each variant.
- No post-init allocations in strict mode: yes, 0 over 1,280,000 events, gated.
- Allocation count unchanged over millions of events: yes — the gate drives
  1.28M per run and the count is exactly 0, not merely bounded.
- Baseline/FBIP/FIP throughput and latency: the table above.
- Compiler/library gaps documented: #9602, #9605 and the E051 ergonomics note
  (#9541), all since FIXED (the sections above are kept as the record of what
  the first measurement found).
