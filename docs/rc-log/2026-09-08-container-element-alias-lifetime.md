# Container element aliases: current-main reproduction

Investigated for #7989, #8920 and coreutils #8278 on main
`0be9c0e8eb767b73f60b037c395f3d736dca59fa`, after #8926 and #8927 merged.
The sections below preserve the red-before evidence alongside the paired fix.
This is ownership validation, not a runtime-speed benchmark.

## CI follow-up: require an initializer witness

PR #8928's self-host shard 0 exposed an unsound admission at `16fca58d8`.
The existing tuple-destructure return test expected its documented residual
leak, but instead reported `allocs=400 frees=400 live_bytes=0`. That particular
fresh-element fixture became clean, but this was not proof of safe admission.

`local_decl_count` counts tuple, foreach and match binders as well as ordinary
locals. The builder validator only checks an initializer when it encounters a
matching ordinary `StmtVar`. A destructured or loop/match-bound return could
therefore satisfy the proof without any validated initializer at all.

Four source-valid regressions reproduced the false `SARRC:build` admission:
fresh tuple destructure, borrowed tuple destructure, foreach-bound return and
match-payload return. Their native source checks passed; all four ownership
proof checks failed before the correction (3.759 s combined suite). The fix
uses the existing local-initializer lookup and requires its value to satisfy
the counted-store/producer contract. Missing initializers remain unknown.
This narrows admission and adds no new projection ownership heuristic.

Afterward the complete original tuple-destructure test, all 26 proof cases,
the ARM64/x86/Wasm lifetime matrix and existing x86 element-reclaim test passed
together in 75.541 s. Lint passed. The tuple test's expectations are unchanged;
its residual projection leak remains work for the typed-IR migration rather
than an unproved widening of this patch. Full CI must validate the new head.
Local logs: `/private/tmp/lang-counted-init-red.log`,
`/private/tmp/lang-counted-init-green.log`, `/private/tmp/lang-counted-init-lint.log`.

## Paired implementation

The follow-up patch is based on main `b4aeb9d2821f64ab2d312b53616b8bb7e3511659`,
after #8925 also merged. It shares typed `StringArraySources` facts between
array-alias lowering and return-ownership inference. Immutable, unshadowed
aliases of borrowed string-array parameters preserve their element provenance.
Their stores take counted references, like direct parameter reads.

`SARRC:` return summaries separately describe a buffer with counted element
references. They propagate through direct and locally bound forwarding calls
to a least fixed point. They enable the receiving local's element walk without
widening `STRARR:` freshness or its projection/field consumers. Every builder
store must carry a fresh or counted claim, and uncounted element/buffer handouts
are refused. Statement walks are exhaustive; unknown statements refuse proof.

This is an immutable-alias slice, not closure of all of #7989. Mutable aliases,
shadowed bindings, unknown callees, borrowed parameter returns and arbitrary
local-element transfers remain outside the new ownership proof. Nested-array
ownership is separate work. In particular, a local binding that shadows a
parameter must not describe that parameter after its scope ends; a new negative
test first demonstrated false admission and now pins its refusal.

The new lifetime cases preserve the full expected bytes under allocator churn
and release all counted references. Direct copies' 64/512/2048 live bytes at
1/8/32 rounds become zero. Array-alias copies retain zero live bytes but now
read the correct values instead of recycled storage. Wasm additionally checks
an exactly flat allocation high-water across two identical exercises.

The neighboring forwarded-result regression now checks the actual ownership
boundary: no element free inside the forwarding function, a deep free in its
consumer, correct values and flat high-water. Its old whole-module assertion
of no element free rejected the newly correct consumer cleanup.

No runtime speedup is claimed. Full compiler, fixpoint, size and performance
validation will run in CI before merge; no baselines were changed.

### Pre-publication validation

- Final targeted run: 106 new cases (72 ARM64/x86 lifetime cases, 12 Wasm
  lifetime cases, 22 ownership-proof cases), plus the complete existing x86
  string-array element-reclaim test, PASS in 56.771 s.
- `make lint-all`: PASS, including source checks and the unchanged feature
  census ceiling. The new statement walkers enumerate all statement variants.
- A broader neighboring run exercised alias binding, counted parameters,
  construction/alias RC, append stores and string-array reclaim. It found the
  old whole-module forwarded-return assertion described above. That assertion
  was replaced with stronger per-function and lifetime checks and the complete
  affected test passed in the final targeted run. All other tests in that
  neighboring run passed; the earlier run itself is not recorded as a pass.
- Full project CI remains the publication-to-merge gate. Neither these local
  checks nor the leak measurements establish overall compiler performance.

## Two distinct failures

The September 2 parameter-element retain handles direct parameter index reads,
string locals bound from those reads, and foreach elements. It does not make
the ownership protocol complete:

| Read path | Values after allocator churn | Allocation outcome, one round |
|---|---|---|
| Direct `names[i]` | Correct | 26 allocations, 24 frees, 64 live bytes |
| String local bound from `names[i]` | Correct | 26 allocations, 24 frees, 64 live bytes |
| `for x in names` | Correct | 26 allocations, 24 frees, 64 live bytes |
| Array local `alias = names`, then `alias[i]` | Corrupted | 26 allocations, 26 frees, zero live bytes |
| Array alias, then string local | Corrupted | 26 allocations, 26 frees, zero live bytes |
| `for x in alias` | Corrupted | 26 allocations, 26 frees, zero live bytes |

All six native-compiled cases produce correct values, with 8 allocations,
8 frees and zero live bytes. The interpreter agrees on values. Self-host
ARM64 and x86-64 agree on both failures above. x86-64 execution used QEMU on
the ARM64 Linux host, for correctness only.

The array-alias variants print `cc?` twice instead of `aa!` and `bb!` after
the source has been released and same-sized string allocations have reused
its storage. Without the explicit churn they appear to pass, even across
32 rounds. All have zero reported RC underflows: freeing a reference too
early need not free it twice. Neither allocation balance nor output without
allocator pressure establishes correctness.

## Minimal shape

```fern
function rebuild(names: string[]): string[] {
    var out: string[] = [];
    var alias = names;
    out = out.append(alias[0]);
    out = out.append(alias[1]);
    return out;
}
function load(): string[] {
    var names: string[] = [];
    names = names.append("aa" + "!");
    names = names.append("bb" + "!");
    return rebuild(names);
}
function churn(): i32 {
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 16) {
        junk = junk.append("cc" + "?");
        i = i + 1;
    }
    return junk.len();
}
function main(): i32 {
    var a: string[] = load();
    if (churn() != 16) { return 98; }
    print(a[0]);
    print(a[1]);
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}
```

Removing `alias` and indexing `names` directly trades the corruption for the
64-byte leak. The test also exercises two simultaneously live rebuild results
from the same source, and repeats the workload at 1, 8 and 32 rounds.

## Emission and ownership facts

The assembly distinguishes the missing releases from missing retains:

- Direct `rebuild` retains each indexed string before appending it.
- `load` already emits `__fern_str_arr_free` for the source. An initial
  hypothesis that it lacked this cleanup was disproved by inspecting the
  assembly, and is not the proposed fix.
- The consuming frame releases the returned array with `__fern_arr_dec`, not the element
  walk. It releases the buffer but strands the strings retained by `rebuild`.
- Array-alias `rebuild` retains the array alias, but not its indexed strings.
  The source's element walk therefore frees strings that the result still
  references. Its apparently clean allocation count is a use-after-free.

`strarr_param_slot_of` recognises only parameter slots. A local array alias
loses that source information, so `str_param_elem_escapes` does not recognise
the element store as a counted share. `LocalInfo.str_caller_elem` records
string-element provenance, not provenance of an array alias.

Separately, `fn_returns_fresh_strarr` and `strarr_local_stores_nonfresh`
require freshly allocated elements. They cannot describe a fresh buffer
containing counted references to existing strings. Forwarding that result
through `load` also needs ownership propagation across the return boundary.

## Required design

Represent buffer ownership and element-reference ownership independently of
uniqueness. A counted reference is not a unique allocation. Do not widen the
existing `STRARR:` freshness contract without auditing every consumer that
uses it for projections, fields and temporary releases.

Track array-source provenance through bindings, reassignment, scopes and
foreach lowering, and use the same facts for bind/store retains and their
matching move/drop decisions. Propagate owned-container return facts through
forwarding calls. Enable deep cleanup only when every stored element carries
the corresponding counted reference. Preserve conservative refusal for
unproven aliases, views, unknown callees and escaping projections.

The acceptance gate is both correct bytes under allocator pressure and
balanced lifetime at increasing round counts. A retain-only patch converts
the alias corruption into the existing direct-read leak; a release-only patch
can turn the leak into corruption. Neither is sufficient.

## Validation state

Regression harness:
`internal/e2eselfhost/self_host_container_alias_lifetime_test.go`.

- ARM64 and x86-64: 72 red-before cases total, six read forms, single/shared
  results, 1/8/32 rounds. Native and interpreter oracles pass. Direct read forms leak
  64/512/2048 live bytes; array-alias forms corrupt output after churn.
- The final harness checks underflows in `main` after `exercise` has returned
  and released its local arrays. Both target matrices complete in 32.459 s;
  this is test duration, not benchmark evidence.
- These are red-before results. The paired implementation above addresses this
  matrix; publication records the final green validation separately.
