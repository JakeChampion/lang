# Reference counting in the self-host compiler

Status: [reference] how the self-host compiler's reference counting works today.

How a Fern function's reference counting is decided and emitted, written for
a reader who knows Perceus but not this code. Each section names the module,
its one job, what it may assume of the stage before it, and the tests that pin
it. History lives in `docs/rc-log/` (one entry per change) and in
`docs/RC-PERCEUS-SELF-HOST-PORT.md`; this page states what is true now, and a
change that makes a section wrong rewrites it in the same diff.

## 1. The pipeline

A function body passes through five stages. Each reads only what the previous
one produced, and nothing lifts an ownership fact from a later stage back into
an earlier one.

| stage | module | in | out |
|---|---|---|---|
| semantic graph | `semsource.fern` (produces), `ssasem.fern` (checks) | typed AST | `ssasem.Func`: an SSA graph of typed semantic operations, no counts |
| ownership inference | `ownership.fern` | every produced graph of the module | per function, which reference parameters it consumes |
| unit plan | `ssaunits.fern` | the graph, the parameter modes | `ssaunits.Plan`: per step, the units acquired, moved and dropped, and the takes, holds, steals, sole boxes and grow rows |
| plan replay | `ssaunits.verify` | the graph and the plan | accepts the plan or names the step that spends a unit it does not hold |
| physical lowering | `ssarc.fern` | the graph, the modes, the plan | stack IR with retains, releases, reuse tokens, takes, steals and grow brackets |
| certifier | `rccertify.fern` (through `irverifygate.fern`, memo in `rcmemo.fern`) | the lowered ops, lifted back to SSA | a clean report, or a refusal naming the unit that is leaked or over-spent |

The seminline pass (`seminline.fern`) rewrites graphs before the plan: it
inlines leaves, splits reads of known constructions into their parts, and
runs tail recursion modulo cons. It is a graph-to-graph pass; the stages
below never see what it removed. Four more run last. `semthread.fern` sends
a branch the path to it already decided straight to the side it takes: a
branch on a constant (the done flag a nested pattern's arms test), and a
variant test an earlier test on the same value, or the same payload of one,
settled. A match whose first arm failed on a payload goes straight to the arm
that payload picks, without testing the scrutinee's variant again.
`semsink.fern` re-reads,
in a block where a projection's parent dies, the counted projections the
block uses that a dominating block read (a record, tuple or payload field,
or an array element at the same index), so the plan takes them instead of
borrowing them. It re-reads only a read that borrowed, of a parent nothing
spends while the child, or the value the block reads through it, lives, so
a re-read never holds a parent longer than the plan already does. Before
that, it merges a repeated read into a read of the same slot in a block that
dominates it, when a read off the first is spent or is live out of the
repeat's block. A `length` does not spend. A record field read used only in
its own block stays apart, because the plan can take it there
(`twin_holds`).
`semcse.fern` then merges a block's repeated reads of one slot of one value,
which the re-reads at a block's head most often make, so the slot has one
holder for the plan to hold or take where its box is consumed. Last,
`semdead.fern` drops constructions nothing reads. `ssasem`'s guard check
treats two reads of one slot of one box (an array's at one index) as one
value, so a test of the first read settles the re-read.

### 1.1 The semantic graph

`semsource` lowers each function to `ssasem.Func`: blocks of `ssa.SInst`
whose kinds are semantic (`record_get`, `tuple_new`, `append`, `with`,
`map_insert`, `call`, phis, …), one type per value, parameters as values
`0..nparams-1`. There is no retain or release in it. A value is immutable:
`append` and `with` answer a new container, and the physical question of
whether that container is the old buffer written in place is the plan's.

A struct-union's member IS its box, so a match arm's binding is the scrutinee
narrowed (`variant_as`), and a field read of that binding is lowered as a
payload read of the scrutinee (`variant_get`), the shape a declared enum's arm
has. The narrowed value itself appears only where the arm uses the member
whole. That is what lets the plan below take a member's fields out of an owned
scrutinee and rebuild the member in the scrutinee's box.

`ssasem.analyze` derives from the graph, and never accepts as a sidecar, the
**dependencies** between values: a projection (`record_get`, `tuple_get`,
`array_get`) is anchored to the container it reads, a value that gathers views
to the storage those views read, and a call result to the arguments its
contract says it may keep (`borrow_parents`). The closure of those chains
(`Closure.dependencies`) is what liveness is computed over (`ssalive`,
validated by `ssadeps`): a container is live wherever a value anchored to it
is. The analysis refuses a cyclic chain and a graph `ssadeps` cannot place.

What later stages may assume: the graph is well formed SSA over a reducible
control-flow graph, every value has a type, and the dependency chains are
transitive and acyclic.

### 1.2 Ownership inference

`ownership.escaping` answers, per function, which reference parameters the
frame **consumes**: a parameter whose unit leaves the frame (returned, built
into a container or box, stored into a cell or map, handed to a callee's
counted slot, or used as the base of a record update, which reuses its box)
is counted, and the caller hands over a unit the callee releases. A parameter
the frame only reads stays borrowed, because a retain and a release on a value
that cannot outlive the call would cancel. It is a greatest fixpoint over the
module's produced graphs: every parameter starts consumed and is demoted when
a round finds no escaping use, so a recursive traversal that rebuilds keeps
its parameter counted and a quiet cycle (functions returning no reference)
does not. A declared `own` stays counted whatever the analysis says, since
the checker's affine discipline (E050) and call sites rely on it; a bare
string or array parameter is deliberately not promoted (`owned_shape`): a
byte kernel reads its argument and hands back an index.

`semsource.with_inferred_modes` rewrites the modes of every produced body and
every callee contract it carries from the same rows, so definition and call
site read one answer. What later stages may assume: a parameter's mode is
final, and a callee contract's modes match the callee's own.

Pinned by `TestSelfHostOwnershipInference`; the measurement that keeps both
halves (`docs/rc-log/2026-09-21-owned-by-default-alone-is-a-regression.md`).

### 1.3 The unit plan

`ssaunits.plan` walks the graph backwards and writes, per **step** (an
instruction, an edge, the entry, a return), which values are **supplied**
(moved into a construction, a callee's counted slot, a store, a return),
which are **dropped** there (their last use), and which supplies were
acquired by a retain because the value is still used after. The step walk
runs over the flow with dependencies, so a borrowed projection keeps its
container alive and a container is dropped where its last anchored read ends.

Before the walk, a family of analyses decides which reads are **units of
their own** rather than borrows. They share one model: every unit has a
holder (the value the walk counts), the values anchored to it borrow its
storage, a move ends it, and a rule that lets a read become a unit of its
own has to show the holder is not needed past a point other than through
what the read took. That question is asked in two relations, both read off
`ssasem.Analysis` and `ssalive`: the **anchored** one, whether the storage
behind a value is still needed (`used_after`), and the **direct** one,
whether the value itself is still in hand (`read_after_direct`, the flow over
`a.parents` alone). Both are answered from two tables on the analysis: what
reads a value and where (`ssasem.Analysis.uses`, each operand with its block,
position and operand slot, queried by `uses_only`, `use_count` and
`read_after`), and what borrows from it, the chains read backwards
(`ssasem.Analysis.anchored`).
`needed_past_slot` is the anchored question asked about
one slot of a box: a later read of the box in the block, a value anchored to
it used after the point, or a block that can run later naming it
(`named_after`, which reads a phi's operand only on its own edge, the edge
its slot names), with a
read of another slot let through. Each rule is a query over those, with its
allowances stated at the call, and folded into the ownership table the walk
reads (`units_of`):

- **Payload take** (`payload_takes`, `slot_takeable`, `needed_past_slot`): a read
  of a reference payload out of an enum, or of a buffer, record, map or
  union out of a record field, a tuple element or an owned array element,
  when the box is the frame's unit and the frame reads it no further through
  that slot. A `with` that stores back at the element read, by the same index
  value or the same literal, is not a read of it. The takes chain: a taken
  element or payload is a unit of the frame's own, so a read of one of its
  slots may take in turn (`taken_root`), which is how an enum held in an
  array hands its payload to the callee that rebuilds it. The lowering tests
  the box's uniqueness at the read (`ssarc.steal`): unique, the slot is
  nulled (`op_opt_set_payload` for the builtin Option layout) and the read
  owns the unit; shared, the read is retained as a borrow would be. A take
  out of a box nobody else sees (`sole_box`) nulls nothing and releases the
  box as a shell (`shell_root`). Pinned by `TestSelfHostPayloadTakeIR`, the
  payload rows of `TestSelfHostSSAPhysicalRC` and
  `TestSelfHostRecordPathAllocs`.
- **Tuple take** (`tuple_takes`, `takeable_root`): the untested form, for a
  tuple this frame allocated (`tuple_new`, `map_delete`'s pair) whose other
  elements are only read: the slot is nulled with no runtime test, since no
  one else can see the box. It asks `needed_past_slot` strictly: the tuple
  must die in the block. Pinned by `TestSelfHostMapTupleElemReclaim*`.
- **Hold** (`held_elements`): a projection of a counted type that is still in
  hand where its source is consumed, or where its source dies unconsumed and
  the read (or a read anchored to it) is consumed later, is retained at the
  read and released at its own death, and its chain to the source is cut, so
  the source moves instead of being retained. A read only borrowed to its
  death stays a borrow. A read that is a take already has its unit and is
  not held as well. An update consumes its donor, so a read of the donor
  used past the update is held, and only that read: the donor is not a
  source that dies unconsumed there. A hold cuts a chain, so the payload
  takes are then asked again over the cut chains (`takes_past_holds`). A
  take from that ask goes in only when its source is owned. There
  the plan can take a second read of a record field. Each read off the first
  read of that field that is still in hand is held for it (`twin_holds`).
  Pinned by `TestSelfHostKeptReadPastUpdate`, `TestSelfHostCrossBlockReuse`'s
  `read-outliving-the-attempt-takes-its-unit`, the `field-read-outlives-the-record`
  rows of `TestSelfHostSemanticAllocationCounts`, and the `update-keeps-fields`
  rows of `TestSelfHostSemanticReuseDifferential`, which pin that a field an
  update keeps is not held.
- **Steal** (`steals`): a direct call's counted slot fed by a field of a
  record this frame owns and reads no further through that field takes the
  field's unit (`steal_unit`) rather than a retained one, so the callee holds
  the buffer's only count and grows it in place. Pinned by
  `TestSelfHostTrmcConsumeIR*` and the `Emit { ...e, r: emit(e.r, op) }` rows
  in `TestSelfHostSemanticAllocationCounts`.
- **Sole box** (`sole_boxes`, `sole_result`): a buffer no unit but the
  frame's own ever holds, from a fresh array, a `with` whose receiver no
  borrowed parameter carries (`lent_withs`), or a fresh row, kept
  sole through passes and phis that preserve it, so a `with` on it writes in
  place without a copy guard. Pinned by `TestSelfHostSoleLoopWithX86_64` and
  `TestSelfHostGrowSoleOccurrenceX86_64`.
- **Grow rows and brackets** (`grow_rows`, `grow_table`, `Plan.grows`): per
  callee, the borrowed parameter positions whose buffer the callee may grow or
  write in place; a caller that hands such a buffer on while it still reads it
  brackets the call (`ssarc.bracket_take` / `bracket_give`): it takes the
  buffer's unit before the call and gives it back after, so the callee's
  append finds a unique buffer. A caller whose read ends at the call hands the
  buffer unbracketed (`Step.hand_roots`). The table is a fixpoint over the
  module. A call through a function value or a dyn receiver names no body:
  its rows are every closure body's (under `indirect_callee`) or every arm's
  (under the dyn call's name), and it holds the whole box of each lent
  operand at those positions, handed parameters included
  (`dispatched_rows`, `ssarc.dispatch_bracketed`). Pinned by
  `TestSelfHostFieldAppendInPlace*`,
  `TestSelfHostGrowFieldBracketReleasesRetainedX86_64`,
  `TestSelfHostLentArrayBracket` and `TestSelfHostDispatchedBracket*`.
- **Kept fields** (`Plan.kept`): the fields a record update keeps, so the
  construction that reuses the record's box leaves them in place.

Once a plan is final, it decides where a construction builds in a dying
box, and those decisions are tables of its own (`Plan.reuse`, `Plan.links`,
made by `with_reuse` at the lowering's entry, once per function rather than
for every plan ownership inference tries):

- **Links** (`links_of`, `Plan.links`): an append or a with threaded
  through a borrowed array parameter, `b = b.append(x); return b.append(y)`,
  `xs = xs.with(0, y); return xs.with(1, x)` or either in a loop, keeps the
  plan's unit but encodes it at run time: while the result is still the
  parameter's box no unit is owed, and once it is a box of the frame's own
  the update writes it in place. A with's first write onto the parameter
  tests the box's count, so it writes in place only when the caller is done
  with the array (the grow row brackets every caller still reading it), and
  copies otherwise. A link is never a reuse donor, since its box may be the
  caller's.
- **Reuse tokens** (`reuse_pairs`, `carried_pairs`, `block_keeps`,
  `Plan.reuse`): a value dying in a block, of the slot count a later
  construction in the block claims, has its drop turned into a **token**
  the construction spends (`ReusePairs.tokens` where it is taken,
  `.builds` where it is spent, `.selfs` for a value dying at the
  construction itself). A value dying after its block's last construction
  may be **carried** to a construction in a later block (`Carry`): the
  blocks holding it, the constructions it is handed to, the edges leaving
  its ways that free it, and the edges on which it arrives null because the
  donor died there as a box of another count. A construction that reads a
  field back from its donor at the same slot, a record's, a declared
  variant's or a struct-union member's, **keeps** the field in place
  (`Keeps`), and every other take
  from that donor follows the one uniqueness read the keep makes. A donor
  whose payloads a block takes and which dies in the block with no such
  construction is **classified** (a keep with no construction, `built` -1):
  its takes and its death follow one uniqueness read. The
  routes and the one-token-slot discipline are decided here, once; the
  lowering only emits what the tables say. `FERN_SELFHOST_NO_REUSE=1`
  (`reuse_layer_disabled`) plans no token, and the program's behaviour is
  unchanged.

The plan is planned once per function and modes. `plan_analyzed` is the
entry with an analysis the caller already holds; `plan` derives it.

### 1.4 The replay

`ssaunits.verify` re-derives the units from the graph, the modes and the
plan's own tables (takes, holds, payloads) and replays every step: a unit must
be moved, dropped or returned exactly once, a borrow never spent, a take or
hold agreeing with what the replay derives. It then replays the tokens
(`verify_reuse`): a token is taken from a value the step drops and never
while the block's one slot is held, spent by the construction it was handed
to, which reads nothing of the box it builds in, a kept donor is its
construction's token, a classified donor holds the slot only where no pairing
does, and a carried token is held on every path into a block
or on none, spent where the carry says, freed where its way leaves, and held
at no return. It is independent of the walk that planned: a disagreement
names the step. It runs under `FERN_IR_VERIFY=1` (the
test harness sets `quiet` for every self-host compile), not in a plain
compile.

### 1.5 The physical lowering

`ssarc.lower` turns the graph and its plan into the stack IR the backends
emit: SSA value ids become locals, parameter ids the ABI's positions, scratch
locals sit above `nvals`. It emits:

- **retains** before a construction or return for every acquired supply, and
  **releases** at a drop: a parent releases its children only under
  `__fern_rc_is_unique`, then its own box (`drop_value`, the per-type
  `__sem_release_<T>` helpers, and `__sem_drop_<T>` for children only); a
  value whose variant a payload read fixes (`ssaunits.known_variant`) is
  released by what that variant holds, in place, and a variant holding no
  counted field by its box alone (`drop_known`, `token_children`); a
  `zero` (the null word the inliner passes for the payloads of a variant a
  path did not build) is dropped with no code at all (`null_unit`);
- **takes and steals** as a uniqueness test, a slot store of zero and the
  value in its local (`steal`); a kept or classified donor's takes read the
  token slot instead, and on its unique arm leave the slot as it is: its
  death releases only the fields nothing took (`keep_release`,
  `classified_death`), and a classified donor's box goes to the release
  helper's `.u` entry with no guard in front (`ir.sole`), since nothing
  between the take and the death can have changed its count;
- **reuse**, as the plan's tables say (`Plan.reuse`): where a token is taken,
  `__fern_rc_is_unique(d)` selects the box or null (`reuse_token`); where it
  is spent, `__fern_alloc_reuse(token, slots)` hands back the box or a fresh
  one, the replaced children are released under the token's non-null arm and
  the kept fields are left in place (`reuse_construct`). A carried token is
  taken where the donor dies (`hold_token`), passed through its way in the
  donor's own local, spent by the construction (`self_token`) and freed on
  an exit edge (`free_token`); a null edge zeroes the local. The lowering
  makes no pairing of its own. Pinned by `TestSelfHostCrossBlockReuse`,
  `TestSelfHostLoopReuseIR*`, `TestSelfHostSemanticReuseDifferential`, the
  enum reuse suites, and the token rows of `TestSelfHostSSAPhysicalRCRejects`
  (a planted table the replay refuses);
- **grow brackets** around calls the plan marked (`bracketed`);
- **string and view** handling: a view is lent, never counted, and released
  through the view's own release where a container holds it. A slice, of a
  string or of a byte array, whose every use reads it within one
  instruction (`ssaunits.frame_views`) builds its descriptor in three frame
  slots (`op_str_slice_frame`, `op_raw_string_frame`) that the release
  skips; on wasm, which has no data pointer to share, the same op copies the
  bytes and the release frees the copy.

What the lowering may assume: the plan verified; it re-derives the semantic
facts it needs from the graph rather than trusting a sidecar. It refuses the
shapes its vocabulary does not cover (a 32-bit float, a record with no schema
or with type arguments) rather than falling through.

### 1.6 The certifier

`rccertify.certify` reads the lowered ops back as SSA (`ssa_lift`) and
nothing of the plan, and accounts per path the units the frame holds: an
allocation or owned call result is a fresh unit, a counted parameter a unit
handed in, a load or a borrowed parameter nothing; a retain adds one; a
release, a move into a callee or container and a return each spend one; at a
join the counts are met per value, and a disagreement is unproved rather than
a finding. A load inside the arm a box was proved unique in (the children
release, a reuse token's non-null arm, a `__sem_drop_<T>` body) is the frame's
to release. A join a reuse token the function branches on decides is walked
again under each of the token's values: the function pruned to the paths
that value selects, from the token's block on, with the token and every phi
the pruning fixes as the box or null, the box's reuse as the box and its
shallow release as a shell where it was unique, seeded with the rows the
walk without an assumption settled; what both walks settle is proved, and
either walk's finding is one. A finding is a definite violation: a unit
spent that the frame does not hold, or a unit it holds when it returns.
Every compile certifies
every function it emits, through a memo keyed by the function's ops, contract
and callee contracts (`rcmemo`, under the user's cache root), so a steady
compile pays only for lowerings it has not seen. `FERN_RC_CERTIFY=0` leaves it
out, `FERN_RC_CERTIFY=report` turns refusals into a census, and a refusal
exits 5 naming the function and the unit. Pinned by `TestSelfHostRcCertify*`
and the `certifier-refuses-missing-retain` row of the physical-RC driver.

## 2. The runtime contract

Every counted value is a heap box with its count word at `data-8`; the data
pointer that flows through the program points past the header.

- An **array** box has its capacity at `data-16`, its count at `data-8`, its
  length at `data+0` and element `i` at `data+(i+1)*8`
  (`__fern_arr_box`). A **record** is an array-shaped box whose first word is
  its `shape_addr` and the rest one word per field; a **tuple** has one word
  per element and no shape word; a declared **enum variant** is laid out as a
  record, its shape word naming the variant. A **string** box is a data
  pointer and a length, released through `__fern_str_free`; a fused string
  carries its bytes behind the same block, and a grown one (the
  self-append's fallback, `__fern_str_grow_fresh`) records the block's
  capacity in the word ahead of its count and is freed at that capacity.
- The count word is a signed 32-bit integer. A count below zero marks an
  **immortal** box (a literal, a static empty array, a `.rodata` closure
  cell): every retain, release and uniqueness test skips it, so a reuse never
  hands back read-only memory. `__fern_rc_is_unique(p)` is 1 only for a live
  heap box with count exactly 1; null and anything below the low-address
  guard answer 0, so it is safe on a slot that may hold a scalar.
- Releases free at the last count through a size-classed freelist
  (`__fern_arr_dec`, `__fern_rc_dec`); `__fern_alloc_reuse(token, slots)`
  hands a dead box of the right class back as the new allocation and files a
  mispaired one on its freelist.
- Under `FERN_SANITIZE=1` (or `-sanitize`) a freed block is poisoned with a
  marker in its count word and never recycled, an over-release and a touch of
  a poisoned block are fatal with a backtrace, and a leak census prints at
  exit; `FERN_LEAKCHECK=1` is the census alone.

The drop helpers the lowering emits per type (`__sem_release_<T>`,
`__sem_drop_<T>`) are the same code a map's column release calls on the
entries it supersedes or walks (`release_helper_name`), so one release
discipline covers values the frame holds and values a container holds.

### 2.1 One context per heap

Counts and the allocator's freelists are not atomic, and stay that way
(#11914; `docs/MULTICORE-RESEARCH.md` C1). The guarantee that makes this sound
is that no two contexts ever run Fern code against one heap. Each boundary
where a second context exists holds it in its own way:

| Boundary | What crosses | What holds it |
|---|---|---|
| `proc_fork`, `subprocess`, the forked `serve` workers | a copy-on-write heap, then pipes, argv and exit codes | separate processes |
| `signal_catch` and its siblings | a signal number | the handler is a fixed stub that sets one flag the program polls; no builtin installs a Fern function as a handler |
| tasks, `Future` combinators, `std/sim` | values in the one heap | one thread; a task parks only at `__task_park` |
| wasm `async function` exports and imports | canonical-ABI copies | the export signature admits scalars only |
| `-shared` exports | C arguments | `__fern_busy`: each export entry takes it and aborts when another thread holds it; a `__c_call` frees it for the call, so a C callback entering an export on the same thread still runs |

`TestX86_64SignalPreemption` and its arm64 sibling hold the signal row under
real preemption. They interrupt heavy count traffic with signals sent from
outside, and require the same output and the same census as a quiet run.
`TestSharedLibX86BusyFlag` holds the `-shared` row: two threads inside the
object at once abort, while threads that take turns and a same-thread
callback both run.

An abort or `exit` ends the process with `exit_group`, not `exit`, so a
report inside a host's thread takes the whole host down rather than one
thread (#12169).

A new boundary (`spawn`, an `interrupt` attribute) lands with its own check,
never as a convention.

## 3. Debugging

In the order they are usually reached for:

1. `FERN_SANITIZE=1` on the compile of the failing program: one flag over the
   over-release trap, the use-after-free quarantine and the leak census. A
   clean run prints nothing. `docs/SANITIZER.md` has the contract.
2. `FERN_IR_VERIFY=1` on the compile: the plan replay, the SSA and schema
   checks and the IR verifiers, exiting 4 at the op the lowering produced
   rather than where the machine code falls over.
3. `FERN_RC_CERTIFY=report` with `FERN_RC_CERTIFY_MEMO=0`: the certifier's
   census over every function, and `FERN_RC_CERTIFY_DUMP=<name>` (or a
   `<prefix>*`) to print the lifted SSA it read, under the report mode and at
   a refusal.
4. `FERN_SEM_DUMP=<function>`: the semantic graph as the inliner leaves it,
   before the plan.
5. `FERN_CLIFF_REPORT=1` on a run of the compiled program: how many appends
   found their array shared with room to spare and how many bytes they copied
   (`__arr_push_shared_count` / `__arr_push_shared_bytes` from Fern;
   `scripts/cliff-bench` for the compiler compiling `checker.fern`). A copy at
   a site means a second holder; `docs/LOCAL-DEV-LOOP.md` gives the gdb
   methods that name it: a breakpoint on the copy sites with the caller
   chain for an attribution by function, and a watchpoint on the box's count
   word for the holder's exact retain and release.
6. `__rc_underflow_count()` from Fern, `FERN_RC_UNDERFLOW_TRAP=1` to make the
   first over-release fatal at its dec, and `FERN_RC_FREE_DEBUG=1` for the
   quarantine alone.

Which suite proves what, and which look authoritative and are not:
`docs/TEST-GATES.md`. The fixpoint is self-referential and blind to a stable
miscompile; `internal/testing/e2ecompiler` is primary on a lowering change.

## 4. What is not here

- The reuse pairing is decided in the lowering, not the plan, so the replay
  does not see it and the certifier is the only check on it (#11909 proposes
  tokens as plan units).
- The analyses in section 1.3 are separate walks with separate soundness
  arguments; their seams are where the imprecisions sit (#11910 proposes one
  forward model they all query).
- The planner's take through a record whose slot is read once, into a field
  of the record it holds, let the compiler's own build free a shared array
  early when a local reuse of identical reads made such shapes common
  (#12135).
- Each shape the native compiler's reuse promised and the self-host port
  preserves is listed in `docs/REUSE-CONTRACT.md` and
  `docs/SELFHOST-PERCEUS-REUSE.md`; the Koka and Lean comparison is
  `docs/PERCEUS-COMPARISON.md`.
