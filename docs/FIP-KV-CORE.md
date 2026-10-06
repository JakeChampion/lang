# Experiment: the bounded key/value core

What #9585 asked, measured. The programs are `examples/fip/kv_*.fern` — three
implementations of one bounded key/value service, differing in how the table
is held — and `internal/testing/e2e/fip_kv_test.go` keeps their claims from rotting.

Read `docs/ALLOCATION-OBSERVABLE.md` first for what the allocation numbers
mean, and `docs/FIP-EVENT-LOOP.md` and `docs/FIP-PACKET-PROTOCOL.md` for the
experiments before this one. This one is about the data structure: the event
loop held 256 entries in parallel arrays and the packet codec held none, so
neither asked what a `fip` plane costs when the state is a real table with
lookup, insertion and deletion, and neither compared it with the maps the
standard library already ships.

## The program

A key/value store with PUT, GET, DELETE and INCREMENT over fixed-size keys (16
bytes) and values (32 bytes), a capacity of 4,096 entries, and requests
delivered 64 to a batch. A request is one record — operation, key, value — and
a response is a status byte followed by the value when there is one. INCREMENT
adds the request value's first eight bytes to the stored value's first eight
bytes, little-endian, and answers the new value. PUT of a new key into a full
table answers `full`; nothing grows.

The workload draws each request's key from a mixing hash over a universe of
`4096 × load / 100` keys, so the load factor is the knob a run turns, and its
operation from one of three mixes:

| mix | GET | PUT | INCREMENT | DELETE |
| --- | ---: | ---: | ---: | ---: |
| read-heavy | 80% | 10% | 5% | 5% |
| write-heavy | 20% | 45% | 20% | 15% |
| mixed | 50% | 25% | 15% | 10% |

Half the universe is PUT before the measured region. A load above 100% is how
the table is driven to capacity: at 150% the universe is 6,144 keys and the
table fills within the first few hundred rounds.

| variant | the table | annotation |
| --- | --- | --- |
| `kv_baseline.fern` | the built-in `Map[string, string]`, keys and values as strings, records in and records out | none |
| `kv_pmap.fern` | `std/pmap`, the persistent hash array mapped trie, same records; `shared` holds a snapshot of the map across each batch | none |
| `kv_fip.fern` | an open-addressing table over preallocated byte regions, requests and responses as byte records | `fip` |

Every variant folds every response byte into one running digest. All of them
answer the same 640,000 requests to the same counters and the same digest, at
every mix and every load, under both compilers — 96 cells, no disagreement.
That agreement is the experiment's differential test; a variant that answered
differently would be caught rather than reporting a throughput win for doing
less work.

## What it measured

Apple M-series, arm64-darwin, one run per cell of 10,000 rounds × 64 requests
(4,000 rounds at 150% load). Spot re-runs of the `fip` cell varied by about
5%. Both compilers built every variant from the same source, and the two
tables below are the same programs under each.

### The mixed mix, by load factor

Built by the Go compiler (`bin/fern`):

| load | live | baseline allocs/req | ns/req | pmap allocs/req | ns/req | pmap-shared allocs/req | ns/req | `fip` allocs/req | ns/req |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 25% | 761 | 32.45 | 914 | 9.29 | 864 | 9.29 | 862 | 0 | 518 |
| 50% | 1,504 | 55.23 | 1,309 | 9.48 | 881 | 9.48 | 878 | 0 | 529 |
| 90% | 2,655 | 91.54 | 1,891 | 9.61 | 899 | 9.61 | 891 | 0 | 530 |
| 150% | 4,096 (full) | 135.16 | 2,467 | 9.46 | 879 | 9.46 | 883 | 0 | 527 |

Built by the self-host compiler (`bin/fern-selfhost`):

| load | live | baseline allocs/req | ns/req | pmap allocs/req | ns/req | pmap-shared allocs/req | ns/req | `fip` allocs/req | ns/req |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 25% | 761 | 6.55 | 323 | 10.20 | 474 | 10.20 | 464 | 0 | 324 |
| 50% | 1,504 | 6.55 | 326 | 10.33 | 477 | 10.33 | 477 | 0 | 328 |
| 90% | 2,655 | 6.55 | 340 | 10.47 | 488 | 10.48 | 507 | 0 | 332 |
| 150% | 4,096 (full) | 6.52 | 327 | 10.27 | 475 | 10.30 | 487 | 0 | 333 |

### The three mixes at 50% load

| mix | compiler | baseline allocs/req | ns/req | pmap allocs/req | ns/req | `fip` ns/req |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| read-heavy | Go | 43.79 | 1,152 | 7.49 | 779 | 490 |
| read-heavy | self-host | 6.36 | 334 | 7.83 | 407 | 316 |
| write-heavy | Go | 57.78 | 1,313 | 11.57 | 967 | 551 |
| write-heavy | self-host | 6.67 | 310 | 12.96 | 564 | 333 |
| mixed | Go | 55.23 | 1,309 | 9.48 | 881 | 529 |
| mixed | self-host | 6.55 | 326 | 10.33 | 477 | 328 |

### Round latency, mixed mix, 50% load

Nanoseconds for one batch of 64 requests end to end.

| variant | compiler | p50 | p99.9 | p99.9 / p50 |
| --- | --- | ---: | ---: | ---: |
| baseline | Go | 83,833 | 110,708 | 1.32x |
| pmap | Go | 56,791 | 73,333 | 1.29x |
| `fip` | Go | 33,750 | 46,750 | 1.39x |
| baseline | self-host | 20,708 | 29,042 | 1.40x |
| pmap | self-host | 30,708 | 42,750 | 1.39x |
| `fip` | self-host | 20,958 | 30,250 | 1.44x |

### What the tables say

**The `fip` table is allocation-free and flat.** Zero allocations in every
cell, including the four at capacity, and a cost that does not move with the
load factor: 518 to 530 ns per request under the Go compiler across a 5x
range of live entries, 324 to 333 under the self-host. The persistent map is
flat too, at 1.6 to 1.7x the `fip` cost natively; the baseline is not, and the
next section says why.

**The self-host compiler's output is faster in every cell.** Not by a little:
the `fip` plane runs 1.6x faster from the self-host build (328 vs 529 ns), the
persistent map 1.8x (477 vs 881), and the baseline 4x (326 vs 1,309). The
self-host compiler is becoming the default, and this is the first of these
experiments to say so with a number on its own programs. Part of the baseline's
gap is the native copy below, but the `fip` plane allocates nothing under
either compiler, so its 1.6x is code generation alone.

**Allocation-free bought a 1.7x constant, not a flatter tail.** As in
experiment 1, the p99.9 / p50 ratio is the same for all three variants under
both compilers (1.3 to 1.4x). What the discipline removes is the per-request
cost; what is left in the tail is the machine.

**The three disciplines keep their order under both compilers, except one.**
Under the Go compiler the order is `fip`, then pmap, then baseline. Under the
self-host it is `fip` and baseline level, then pmap: the built-in map's
in-place path is a hash table write, which is what the `fip` table does by
hand, and the persistent trie pays its path rebuild either way.

## What the language forced

### A map in a field of an owned struct copies itself on every write (#11119)

The natural shape — one `Db` struct with the map in a field, rebuilt with
`Db { ...db, entries: db.entries.insert(k, v) }` — measured **1,344
allocations and 19 µs per request** under the Go compiler, 36x the `fip`
plane. Every write deep-copied the whole map: `computeMapCowForcedCopies`
forces the copy for every mutator whose receiver is a field, with no excusal
for the rebuild that consumes the struct and supersedes the field, which is the
excusal the array `.with` path has had since #9605. The first version of that
baseline, which borrowed the struct instead of owning it, exhausted the 16 GiB
arena.

The baseline therefore threads the map as a value of its own beside a
`Counters` struct. The persistent map in the same field position cost 12.7
allocations per request against 9.5 threaded — the trie copies its path
whether or not the root is shared — so for a user-level structure the field
costs a third more rather than two orders of magnitude.

### A map mutator inside a tuple literal is miscompiled (#11121)

`process_batch(own entries, req) -> (entries, response)` was the shape the
issue asked for. Returning `(entries.insert(k, v), rsp)` from an `own`
parameter **segfaults**: the tuple is not one of the positions the COW-seam
retain covers, so the tuple and the receiver's binding share one count and
both release it. The sanitizer names a use-after-free; the interpreter runs the
program correctly. The three-element form `(entries.insert(k, v), counters,
rsp)` does not crash and **silently empties the map** instead. Binding the
result to a local first is correct but copies the whole map per call.

Deletion has the same split: five spellings of `without`, two of which copy
the table per call (the issue has the table). The baseline is written as a
read-only `classify` followed by an `update` that returns the map and nothing
else, one mutator per function, with `without` destructured — the one
arrangement of this operation set the native analysis takes in place.

### What the native analysis still copies

Even in that arrangement the Go compiler's baseline climbs with the table: 32
allocations per request at 761 live entries, 135 at 4,096, where the self-host
build of the same source is flat at 6.5. The forced-copy analysis judges a
receiver's last use by source order across the whole function rather than per
path, and a mutator that is not textually last in its function copies. The
self-host lowering does not make that mistake, which is why its baseline is
the cell that surprised: the idiomatic map, written plainly, runs at the speed
of the hand-built table.

### A tuple-destructured local is refused as an `own` argument (#11120)

`let (next, rsps) = process_batch(db, reqs); db = finish(next, rsps);` is E051
at `next`, where the same program with a single-value return is admitted. The
`CallArgDeaths` analysis that admits a plain local at its last use does not
reach a destructured one. The baseline folds the digest inside `process_batch`
instead, which is also what the `fip` variant does.

### The persistent map's reuse recovers an eighth of the path

The issue asked whether Perceus reuse makes a persistent structure
competitive. Measured directly, 1,000 updates of existing keys in a
1,500-entry `PMap[i64, i64]`:

| spelling | Go compiler | self-host |
| --- | ---: | ---: |
| `m = m.insert(k, v)`, unique | 3,821 | 8,642 |
| through an `own` parameter | 3,821 | 8,642 |
| with a live snapshot held | 4,290 | 8,642 |
| inserting new keys | 6,463 | 9,708 |

A unique update of an existing key allocates 3.8 blocks natively; holding a
snapshot — the shape that forces every path node to copy — adds 0.5. So the
reuse pass recovers about an eighth of a path rebuild, not the rebuild. Under
the self-host it recovers nothing: unique and shared allocate the same 8.6
blocks. That is why `pmap` and `pmap-shared` are the same column in every table
above: the sharing a reader's snapshot introduces costs almost nothing extra
because the unique path was already paying for its nodes. `std/pmap`'s header
promises the unique path "allocates nothing"; the number says otherwise, and
that is a stdlib-and-compiler finding for #8920 rather than a flaw in the
experiment.

### Byte copies are one struct rebuild each

A `fip` function cannot hand a field of its owned struct to an `own` parameter
(E051; #8171 item F), so writing a 16-byte key into the table's key region is
sixteen rebuilds of `Db { ...db, keys: db.keys.with(i, b) }`, and a PUT is
forty-eight plus the response. Each rebuild is reuse-paired and writes in
place, and the whole PUT still lands inside 0.5 µs; but it is the reason
`kv_fip.fern` reads as it does, and a byte-range write builtin admitted inside
`fip` would remove most of its lines.

### Full-capacity behaviour is a table-size decision

The first `fip` table had one slot per entry. Below capacity that was fine; at
150% load, with the table full, every miss walked all 4,096 slots and the
mixed mix cost **1,935 ns per request** (2,808 write-heavy) against 530 below
capacity — and a deletion from a table with no empty slot looped forever until
the walk was bounded. The table now has 8,192 slots for 4,096 entries, so it is
never more than half full, and the 150% column reads the same as the others.
The defined behaviour at capacity is: PUT of a new key answers `full`, every
other operation runs at the uncontended cost, and nothing is retried or grown.

## Answers to the questions #9585 asked

**Is `fip process_batch(own database, own requests) -> (database, responses)`
expressible?** Not as written: a `fip` function cannot return two owned
containers (a tuple literal has no donor), and even outside `fip` a mutator
result in a tuple literal is miscompiled today (#11121). The closest form, and
the one `kv_fip.fern` uses, owns the response region inside the database and
borrows the request batch.

**Does the bounded table beat the conventional structures?** Yes: 1.7x the
persistent map and 2.5x the built-in map under the Go compiler, and zero
allocations against 9.5 and 6.5. Under the self-host compiler the built-in map,
written in the one shape its analysis takes in place, reaches the table's
speed with 6.5 allocations per request — so the table's advantage there is
the allocation guarantee rather than the clock.

**Does Perceus reuse make a persistent structure competitive?** It takes a
persistent map to within 1.7x of a hand-built table, flat across load factors,
which is competitive for a structure that also gives you free snapshots. But
the reuse itself contributes little: the measured unique path allocates most
of what the shared path does.

**How does sharing affect reuse?** A live snapshot added half an allocation per
update natively and nothing under the self-host; in the service it was
invisible. The sharing that mattered was the accidental kind: a field read of
an owned struct, which shares the whole map for the duration of the call and
cost two orders of magnitude.

**Can API design preserve uniqueness?** Yes, and it has to: threading the map
as its own `own` value, one mutator per function returned directly, and the
destructuring spelling of `without` are what make the baseline's numbers
honest. Each of those is a shape the compiler should not need, and #11119,
#11120 and #11121 are the three places it currently does.

## Acceptance, against #9585

- Defined full-capacity behaviour: yes — `full` on a new key, uncontended cost
  for everything else, measured at 150% load in every variant.
- FIP steady state: yes — 0 allocations over 640,000 requests at every load
  and mix, under both compilers, gated.
- Mixed, read-heavy and write-heavy benchmarks at several load factors: the
  tables above, 25% to 150%.
- Analysis of sharing and reuse: the persistent-map section and the three
  compiler findings.
- Compiler and library gaps documented: #11119, #11120, #11121 filed with
  reproducers; the `std/pmap` reuse measurement recorded above for #8920.
