# The sanitizer build (`-sanitize` / `FERN_SANITIZE=1`)

Fern's heap memory-safety detectors used to be three separately-named
environment variables. Each was sound; together they were unusable, because
using one meant already knowing which bug you had. `-sanitize` is the single
opt-in surface over all of them — the thing you reach for *before* you know.

```sh
fern -target x86-64-linux -sanitize -g -o prog prog.fern && ./prog
# or, for a driver you don't invoke directly:
FERN_SANITIZE=1 go test ./internal/e2e/ -run TestWhatever
```

Both spellings do the same thing. The flag is read at **compile** time — it
changes the code the backend emits — so it goes on the build, not on the run.

## What it turns on

| Check | Fires when | Output |
| --- | --- | --- |
| rc over-release (double free) | a `dec` sees a refcount already `<= 0` | `fern-sanitizer: rc over-release (double free)` + backtrace, exit 124 |
| use-after-free | an `inc`/`dec` touches a quarantined block | `fern-sanitizer: use-after-free (touched a quarantined block)` + backtrace, exit 124 |
| leak census | the process exits with live bytes outstanding | `leakcheck: …` summary + `fern-sanitizer: leak <K> bytes in <N> blocks` |

**A clean run prints no `fern-sanitizer:` line at all.** That is the whole
pass condition — you do not have to read the census numbers to know whether
the program was clean. Exit code and stdout are untouched on a clean run, so
a sanitized binary drops into an existing harness unchanged.

The two fatal checks route through `__fern_report`, the same abort path the
bounds and arena checks use, so each names its cause on stderr and prints the
frame-pointer backtrace under it. Build with `-g` and the addresses resolve to
functions through `addr2line` / `nm`. Under gdb, `break __fern_report` stops
with the offending frame still on the stack — the old bare-`ud2` recipe's
answer, without needing gdb to get it. (`-backtrace=false` suppresses the walk
for size-critical builds — see `ARRAY-BOUNDS.md`. Turning it off under
`-sanitize` leaves each finding named but unlocated, which is the opposite of
what this mode is for.)

**Integers need none of this.** They are total and never-trap by policy
(`INTEGER-SEMANTICS.md`), so unlike C there is no integer UB to sanitize. The
surface is purely the heap/rc correctness that Perceus's manual `inc`/`dec`
makes possible to get wrong.

## What it does NOT turn on

`FERN_RC_TRACE=1` — one stderr line per heap event — stays separate. It is a
tool you point at a reduced repro after the sanitizer has told you there is
something to look for, not something a standing mode can afford. Compose them
by hand when you want both.

The individual flags (`FERN_LEAKCHECK`, `FERN_RC_UNDERFLOW_TRAP`,
`FERN_RC_FREE_DEBUG`) all still work on their own for exactly that kind of
narrow probe, and compose with `-sanitize` — the fold-down only ever turns
checks on.

## Cost

Real, and the reason this is opt-in:

- **Nothing is ever recycled.** The quarantine is what makes use-after-free
  detectable — a recycled block would overwrite its own poison — so
  `__fern_free` accounts the release and declines to push the block onto a
  freelist. The heap only grows. A loop that churns 200 rows through one
  recycled block instead bumps 200 blocks' worth of arena.
- Every `inc`/`dec` takes the out-of-line helper (the inline rc fast path is
  bypassed, since it is the helper that carries the poison check).

With the flag **off**, none of this exists: every check is unemitted and the
asm is byte-identical to a build from a compiler that never had the feature.
`TestSanitizeOffEmitsNoSymbols` pins the cheap proxy for that.

The wasm census is the cheap end of all this: two counters and one line, with
the freelist still recycling, because the quarantine that carries the rest of
the cost is not built there.

## Backend coverage

| | leak census | rc over-release | use-after-free |
| --- | --- | --- | --- |
| x86-64 (native) | ✅ | ✅ | ✅ |
| arm64 (native) | ✅ | ✅ | ✅ |
| x86-64 (self-host) | ✅ | ✅ (no backtrace) | ✅ (no backtrace) |
| wasm | ✅ | — | — |
| arm64 / wasm (self-host), every SSA backend | — | — | — |

Every row above emits the **same** message text and the same exit status — a
`fern-sanitizer:` line does not tell you which compiler produced the binary,
which is the point: "build it with `-sanitize`" has to mean one thing.

`-sanitize` on a target that is not fully covered **warns and names what the
build does carry**, so a silent run is never mistaken for a checked one. On
wasm that reads "the leak census only, not the rc over-release or
use-after-free detectors"; on an SSA backend, or a target with no
instrumentation at all, it says the build carries no checks.

The **self-host** compiler reads `FERN_SANITIZE=1` at emit time (there is no
`-sanitize` flag on its driver; the env var is the surface, matching its
existing `FERN_LEAKCHECK` / `FERN_RC_TRACE` ports), so the flag goes to the
compiler process, not to the program it produces:

```sh
FERN_SANITIZE=1 bin/fern-selfhost -target x86-64-linux /ABS/prog.fern $PWD/internal/stdlib -o prog.s
```

Its one gap versus native is an honest subset, not a silent difference: no
backtrace under a report, because there is no `__fern_report` equivalent in
that runtime, so the message is the whole diagnostic. This matters because the
self-host compiler is precisely the long-running, allocation-heavy program the
mode was argued for.

Its quarantine also moves one diagnosis: the `__alloc_u8` + double-`__rc_dec`
probe reports **use-after-free** there and **over-release** on native, because
this runtime's `__rc_dec` maps to the freeing `__fern_arr_dec` — the first dec
reclaims and poisons, so the second touches the poison, where native's plain
`rc_dec` never frees and reads rc 0 instead. Each fires for what actually
happened in its runtime; the texts are identical.

One census reading that surprises people: `__free(ptr, size)` is a **no-op** in
the self-host runtime (`irlower.fern` — "a no-op under the bump/leak heap"), so
a program that raw-allocates and raw-frees reports every block as leaked there
while native reports it reclaimed. That is the census being accurate about the
runtime it is measuring, not a divergence to paper over.

## wasm: the census, and only the census

The wasm backend carries the **leak census** — the same line, the same verdict
text, the same "a clean run says nothing" contract:

```sh
FERN_LEAKCHECK=1 fern -target wasm32-wasi -o prog.wasm prog.fern   # census
FERN_SANITIZE=1  fern -target wasm32-wasi -o prog.wasm prog.fern   # + verdict
```

It costs two counters in two helpers, because this runtime has exactly two
chokepoints: every allocation reaches `__fern_alloc` (the box / rc1 / u8-array
wrappers all forward to it) and every reclamation reaches `__free`. Both
count at the same `(size+15)&-16` rounding, so a block's allocation and its
release cancel exactly, and a block that the quarantine-free freelist recycles
is counted once each way.

Three things about it are specific to this backend:

- **Its counters are reserved unconditionally.** They are scratch slots in the
  module's reserved low-memory window, chained off the end of it like the
  rc-underflow counter and the append-cliff pair already there. Nothing WRITES
  them with the flag off, and no census code is emitted — but the addresses
  above them shift, so a flag-off wasm module is not byte-identical to one from
  a compiler predating the census the way a flag-off native binary is.

- **The report is latched.** Wasm's exit seams NEST — the synthesised
  `_start` / `_lang_run` wrapper calls `__fern_exit`, which is also where the
  `exit()` builtin lands — so `__fern_lc_report` prints at most once per
  process rather than once per seam.
- **A module with no exit seam never reports.** A `wasm32-wasi-http` handler
  component is invoked per request and does not exit, so its counters tick and
  nothing prints. That is the same rule as a native server that never returns
  from `main`, and it is what the `-sanitize` warning for that target says.

What wasm does NOT carry is the two fatal checks. Both need a poisoned rc word
and an abort path, and neither is built here — so on wasm a silent run means
"did not leak", not "clean". The rc over-release COUNTER is still there
(`__rc_underflow_count()`), it is just not a report.

## Editing notes

One asymmetry worth knowing if you are editing this: arm64's `__fern_str_inc`
**inlines** its rc bump instead of tail-calling `__fern_rc_inc` (it has to
preserve the `(data, len)` pair in x0/x1), so it carries its own poison check.
Any future helper that inlines an rc op rather than calling out needs the same
treatment, or a stale reference walks straight past the detector.

## Reading a finding

A leak verdict says how much and how many, not where. Pair it with
`FERN_RC_TRACE=1` on the same build to get one `rctrace a …` line per
allocation carrying its call site AND the frame above it; the allocs that
never match an `f` line by pointer are the leak, attributed to the code that
made them. Read the second frame whenever the first names a runtime helper or
a function large enough to have been inlined into — `ir.Inline` runs twice in
every backend battery, and the site field names where code ENDED UP, not who
wrote it. See `TEST-GATES.md` for the pairing recipe and its caveats.

An over-release or use-after-free names a frame directly, and that frame is
almost always the answer: the function whose `dec` was wrong, or the holder
whose reference outlived the count.

## What this does not catch

- **Cycles.** A reference cycle is a leak the census reports as live bytes
  with no further attribution. Identifying cycle members (trial-decrement over
  the leak set) is the unbuilt half of #5362.
- **Uninitialised reads.** Poisoning fresh allocations with a sentinel is
  cheap; distinguishing a read of an unwritten slot from a read of a
  legitimately-poison-valued one needs shadow memory. Neither is built.
- **A stale reference that never touches the block again.** The detector fires
  on the `inc`/`dec`, so a dangling pointer that is only ever *read through*
  raw-memory builtins is invisible.

Note also that the over-release guard sits **ahead** of the free on the `dec`
path, so a refcount that drifts low is usually reported as a double free
before it can become a dangling reference. That ordering is deliberate — it is
the earlier and more precise of the two diagnoses.
