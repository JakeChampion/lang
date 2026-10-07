# Building bounded applications with FIP and FBIP

Start with an ordinary implementation and a workload that checks its answers.
Use FIP or FBIP when a checked allocation discipline helps express the
application's resource limits. Measure the resulting program: an annotation
does not determine which algorithm or representation runs faster.

The [recorded mainf939 measurements](FIP-MAINF939.md) make this concrete.
Ordinary ETL makes no steady allocations and has the
fastest median at each measured batch size. DSP's ordinary direct loop also
beats the annotated graph in every measured group, while both allocate nothing
on unique inputs. Bounded simulation improves on the tested persistent-vector
implementation. These are results for the recorded workloads and compiler,
not a ranking of language features.

Compiler provenance matters. The mainf939 refresh measures all five applications,
ring comparisons and configurable KV with the same exact primary compiler.
Earlier main11a and main546 results remain historical evidence. Use each report's
compiler and workload when reproducing a result or deciding whether a later
compiler changes the tradeoff.

## What the contracts mean

`own` gives a parameter a reference it may consume. It does not prove that the
value has no aliases. The language keeps immutable value semantics: when a
replacement cannot reuse unique storage, the runtime must preserve the old
value, which can require a copy. A snapshot retained across a call is therefore
both a semantic obligation and a useful performance control.

The current [IR budget verifier](../compiler/irfipverify.fern) counts unpaired
allocation sites. Bare `fip` and `fbip` have an allowance of zero; a reuse-paired
rebuild is admitted. A shared-input allocation inside the runtime reuse
fallback is not charged as a fresh site by that verifier. `fip(n)` and
`fbip(n)` allow up to `n` fresh sites. That number is not a bound on total
allocations across a loop or the application's lifetime.

The call graph is checked too. FIP calls must satisfy the FIP call rules; FBIP
can call FIP or FBIP helpers. Selected nonallocating builtins are admitted.
An ordinary helper that happens to allocate nothing is not automatically an
admissible call from either annotation. E053 checks admitted source forms;
E068 reports a lowered allocation that exceeds the declared budget. The
[compile-path tests](../internal/testing/e2ecompiler/self_host_fip_budget_test.go)
include rejected fresh constructors, accepted donor rebuilds and graded
allowances on all required targets.

Choose FIP for a transition whose helper graph can meet the stricter call
contract. Choose FBIP where the intended graph uses FBIP helpers and verified
reuse. Both require runtime allocation checks before claiming that a particular
execution allocates nothing. Keep ordinary code where the contract adds little
value or requires a worse representation. There is no need to label every
function in an application.

## Put initialization outside the transition

List the application's resources before writing the loop: input slots, output
slots, scratch space, request descriptors and retained state. Set their maximum
sizes explicitly, validate those sizes, and construct the initial state in an
ordinary function. A transition then consumes the current state and returns its
replacement. Read-only inputs can remain borrowed.

[DSP processing](../examples/fip/dsp_fip.fern) is a small example. Its public
boundary validates the input count before running gain, filter, delay, mix and
limit over a graph whose buffers already exist. An invalid block returns a
refusal without partially processing samples. Filter and delay history persist
across blocks; resetting that history for each callback would change the work.

Decide whether the contract starts at the first operation or after warm-up.
Storage, broker and DSP test allocation from their first operation after
initialization. ETL and simulation also have warmed measurements; their reports
separate warm-up from measured counters. Warming an allocator can flatten fresh
byte growth while leaving allocation handoffs on every iteration. Check both.

Startup is part of application cost even when excluded from the hot region.
Report it separately. Preallocation trades initialization and reserved capacity
for bounded transitions; it does not make those costs disappear.

## Preserve the replacement path

Read a state field through a borrowed helper when the reader does not need to
keep it. Pass the state to the operation that produces its replacement, then
use that replacement for the next operation. Functional updates such as the
existing `Graph { ...g, work: g.work.with(i, value) }` in
[DSP](../examples/fip/dsp_fip.fern) express which box and buffer are candidates
for reuse without exposing mutation to a caller.

Keeping an old state, array or message alive across the transition changes the
ownership case. Do that deliberately when the application needs a snapshot.
Do not remove the snapshot to satisfy an allocation assertion. The
[simulation driver](../examples/fip/simulation.fern) measures both cases and
reads the old world after the shared update to keep its lifetime real. Its
unique control performs the same extra checksum traversal before the update,
so the two measurements account for that work explicitly.

Sharing is local to what is retained. The [broker](FIP-BROKER.md) keeps a message
alive across replacement; it does not retain the whole broker. Simulation keeps
the world. Storage keeps the state. Their allocation counts answer different
ownership questions and should not be compared as though they were the same
snapshot experiment.

The current reports also record awkward source forms. ETL originally needed
sequential field spreads to obtain a recognized donor for its scratch record.
Treat such a finding as evidence about that tested compiler and source shape,
not a permanent language restriction. Prefer the readable form when the
current compiler verifies it and the runtime measurements support it.

## Make capacity part of the public API

A bounded queue needs a policy for full input, full output and partial work.
An array's allocated length alone does not specify those policies. Test the
empty case, exact capacity, one beyond capacity, retry after refusal and
snapshots of the state before each transition.

The [configurable key/value successor](FIP-KV-CONFIG.md) accepts entry capacity,
key width, value width and batch limit at startup. They belong to each database,
so differently configured instances can coexist. PUT at full capacity still
replaces an existing entry; only a new key receives FULL. A malformed batch
changes neither entries nor the previous response. These policies are part of
the API, not consequences callers should infer from the backing array.

The [broker](../examples/fip/broker_core.fern) checks fanout capacity before
publishing. If one subscriber cannot accept a message, none receives a partial
publication. Lease and acknowledgement state control when a pooled message can
be replaced. Retaining an external message still preserves its old payload.

[Storage](../examples/fip/storage_fip.fern) separates submit, advance and collect.
A completed request continues occupying its slot until collection. Otherwise
a stalled consumer could silently turn a bounded request queue into unbounded
completion storage. Ticket checks reject stale handles. A failed flush leaves
dirty data available for retry; successful completion is what changes the
simulated durable contents. See the [fault and capacity report](FIP-STORAGE.md)
for exact error and exhaustion behavior.

Bound work as well as storage. Storage's scheduler has a visit budget and
bounded page/frame scans. Simulation's grid reduces typical candidate work but
retains a dense N-squared worst case. A capacity limit can bound that worst case
without making it fast enough for a deadline. Measure the clustered or saturated
case that exercises it.

## New data needs a place to live

A new record can be built in an ordinary initialization or admission phase,
written into preallocated storage, or admitted by an explicit graded contract.
Choose according to who owns the result and how long it must live. Returning a
fresh boxed result from each strict transition is a different requirement from
returning the consumed state with scalar status fields updated.

ETL demonstrates three useful choices in
[its implementations](../examples/fip/etl_variants.fern). The ordinary baseline
decodes each record, the batched version materializes a collection of decoded
records, and FBIP reuses scratch state. The scalar FIP path reads fields directly
from the wire. All use the same validation and aggregation rules. With the
measured newer compiler, scalar replacement removes the baseline's apparent
record allocations. Keeping the original baseline revealed that improvement;
rewriting it just to maintain an allocation contrast would have hidden it.

Do not force a FIFO onto an out-of-order completion model merely because a
bounded FIFO is available. Extract a shared collection when multiple actual
callers need its semantics, then measure the abstraction against their inline
implementations. Capacity handling and alias lifetime belong in that comparison.

The [shared ring experiment](FIP-RING.md) extracts a real need from the event
loop and broker into an example-local `Ring[T]`. Its caller supplies a sentinel;
dropping an entry releases the queued reference by replacing it with that value.
Payload construction remains the caller's cost. The generic broker was slower
than its inline control in every mainf939 turnover group median; two refusal
groups favor the generic ring with overlapping ranges. The event-loop successor
also drains responses one at a time, while the packed original resets its count.
Correctness and allocation are checked for that successor, but event-loop
throughput is unmeasured. Reuse of source code is not evidence of faster execution.

## Text and allocating boundaries

The packet and in-memory request examples use explicit byte lengths, offsets
and caller-owned output buffers. They can validate input and encode bounded
responses without constructing a new string for every field. Encoding must
still check output capacity, integer limits, malformed input and truncation.
Fixed-format byte processing is not a replacement for general Unicode handling.

Inspect the exact library API before calling it from a strict transition.
For example, current [`std/string`](../internal/stdlib/std/string.fern)
`starts_with`, `find` and `trim` are ordinary functions, so their names alone
do not establish FIP-call admission. An operation that returns an owned string
may require allocation even when the parser's byte scan does not. Tests of
[frame string views](../internal/testing/e2ecompiler/self_host_str_view_frame_ir_test.go)
cover a particular optimization; they do not establish that every escaping
slice is allocation-free on every target.

Keep formatting, logging, configuration loading and allocating library/I/O
calls outside a strict transition when their contracts require it. Hand the
transition a bounded input, then let the surrounding ordinary code deliver its
result. Account for copies and retained buffers at that boundary. These
in-memory experiments do not measure sockets, disk hardware or production I/O
latency.

## Batch the work, then measure what batching changed

A batch amortizes dispatch and bookkeeping, but increases the amount of work
in one call and can delay an individual record. Report the batch size alongside
throughput and tails. A batch percentile is not a per-record latency percentile.

The [ETL refresh](FIP-MAINF939.md#etl) compares batches of 1, 64 and 1024 at the
same record count. Materializing decoded records still allocates; the ordinary
baseline, FBIP and FIP do not in those measured runs. A larger batch is therefore
not synonymous with fewer allocations. DSP block sizes and storage queue depths
also change the amount of work represented by a sample; keep their units visible.

The configurable KV tape contains complete mixed, read-heavy or write-heavy
operation cycles. Increasing batch size changes both grouping and tape length,
so it is a whole-workload comparison rather than a fixed-tape batching-only test.
Its key-domain percentage is also distinct from table occupancy. The independent
oracle reports actual occupancy and FULL responses. Keep these distinctions in
the report instead of treating a requested load percentage as a measurement.

## Measure the subject, not the bookkeeping

Use scalar allocation marks around the intended region. Preallocate sample
storage, initialize input and state, and perform any declared warm-up before
those marks. Read the final counters before formatting reports or sorting
samples. The [`std/bench` helpers](../internal/stdlib/std/bench.fern) and the
application drivers show both callback and state-threading approaches.

`__heap_alloc_count()` counts blocks handed out by the allocator, including
free-list reuse. `__heap_bump_bytes()` records fresh high-water growth. Neither
is a count of bytes copied, and fresh growth is neither total requested bytes
nor live heap size. Process RSS answers another question. The normative
[allocation claims](../spec/semantics.md) identify the supported observable
behavior; use target execution, not the interpreter, for arena measurements.

The experiment harnesses record exact compiler, binary and source hashes,
configuration, independent oracle results and per-run measurements. They check
every exported sample and recompute nearest-rank percentiles. Keep a small pilot
that exercises the whole pipeline before increasing record counts, queue depths
or repetitions. Change one scale parameter at a time so a failed larger run
has a useful reference.

Collect enough samples for the tail being reported and retain the maximum.
Report repeated-run ranges beside a median; these ranges are not confidence
intervals. Equal tiny percentiles can reflect timer resolution. QEMU results
establish target correctness and allocation behavior, not native throughput.
Whole-process CPU/RSS/fault totals in these harnesses include startup, sample
export and sorting. Page faults are not hardware cache misses.

Zero allocations do not establish real-time behavior. Scheduler delays, copying,
cache effects and worst-case algorithmic work remain. The older event-loop and
in-memory request experiments lowered absolute tails without demonstrating a
flatter tail-to-median ratio. Their measurements do not isolate a cause for
the remaining tail. Storage reports processing time separately from simulated
logical completion latency, which has no direct conversion to device latency.

## Evidence to use for a design decision

| Experiment | What to inspect | Evidence scope |
|---|---|---|
| [Event loop](FIP-EVENT-LOOP.md) | Bounded admission, named versus packed state, full queues | Historical report; original controls retained |
| [Packet codec](FIP-PACKET-PROTOCOL.md) | Borrowed input, overlapping versus separate output, copying | Historical report; malformed and maximum-length cases |
| [Key/value core](FIP-KV-CORE.md) | Load, full tables, persistent snapshots, representation cost | Historical compiler comparison; not a current speed claim |
| [Configurable key/value successor](FIP-KV-CONFIG.md) | Runtime dimensions, mixed workloads, full refusal and whole-state sharing | Main546 history; [mainf939 refresh](FIP-MAINF939.md#configurable-keyvalue-workloads) |
| [In-memory request pipeline](FIP-HTTP-APP.md) | Parse/route/update/encode and explicit text limits | Historical in-memory experiment, no networking implementation |
| [ETL](FIP-ETL.md) | Equivalent decoding, filtering, aggregation and batching | [Mainf939 refresh](FIP-MAINF939.md#etl); ordinary baseline leads |
| [Simulation](FIP-SIMULATION.md) | Unique/shared worlds, dense work bounds and layout | [Mainf939 refresh](FIP-MAINF939.md#simulation) includes all layouts |
| [DSP](FIP-DSP.md) | Block partitioning, persistent filter state, direct-loop control | [Mainf939 refresh](FIP-MAINF939.md#dsp-broker-and-storage); direct loop leads |
| [Broker](FIP-BROKER.md) | Atomic fanout, pool leases, saturation and retained messages | [Mainf939 refresh](FIP-MAINF939.md#dsp-broker-and-storage) |
| [Storage](FIP-STORAGE.md) | Bounded descriptors, dirty eviction, faults and completion backlog | [Mainf939 refresh](FIP-MAINF939.md#dsp-broker-and-storage) |
| [Shared bounded ring](FIP-RING.md) | FIFO extraction, nested ownership, payload lifetime and abstraction cost | [Mainf939 refresh](FIP-MAINF939.md#shared-bounded-ring); event-loop throughput unmeasured |

Earlier reports contain compiler limitations that have since been fixed,
including issues #9700, #9702, #11119 and #11121. Read those passages as the
history of the experiment. The string-view issue #6713 likewise does not justify
a current blanket claim about slicing. Consult current target tests and verify
the actual call path before designing around an old defect.

The refreshed evidence demonstrates why the ordinary control must remain in
the benchmark. Compiler improvements can remove an allocation that once
motivated hand-written storage. A persistent representation may still be the
right choice when long-lived snapshots are central; a direct loop may be the
right choice when a graph adds traversal and reconstruction work. Keep the
checked resource boundary where it helps, and choose the algorithm using the
application's answers, limits and measured costs.
