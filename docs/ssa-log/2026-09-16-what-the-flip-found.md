# What the arm64 default flip found that the corpus differential could not

**Date:** 2026-09-16

The flip (`docs/ssa-log/2026-09-16-arm64-default-ssa.md`) was made on 348
corpus programs agreeing across both backends, all 28 bench programs at or
under the stack machine, and `cmd/fern` green.

That was not enough, by a wide margin. What the broader suites found, in the
order the evidence arrived:

| round | found by | what |
| --- | --- | --- |
| 1 | `internal/e2e -run 'Arm64\|CLI\|Args\|Native'` | a silent abort, a verify gate that never ran, two programs that stopped compiling |
| 2 | the #9520 sweep, which finished after that PR had merged | a test asserting an aborting program writes nothing |
| 3 | another session, bisecting a red aarch64 shard (#9525) | four runtime helpers the self-host compiler needs |
| 4 | `test-units-aarch64`, 161 failures | five more the coreutils need |
| 5 | the full `internal/e2e` sweep | a tenth, reached only by e2e fixtures |

**Ten runtime helpers had no emitter.** On an aarch64 runner every
default-target compile routes through this backend, so the flip turned a
standing coverage gap into a red `main`.

## The first round

### A fatal abort wrote nothing to stderr

```
$ fern -target arm64-linux -backend flat -o oob oob.fern && qemu-aarch64 oob
fern: array index out of range
exit=134

$ fern -target arm64-linux -o oob oob.fern && qemu-aarch64 oob
exit=134
```

The exit code survived; the diagnostic did not, so the program looked like it
had taken a signal. `TestArm64AbortMessages`, three of four cases — the
in-bounds control passed, so the trap fired and said nothing.

The same gap was on x86-64-ssa. Fixed on both: one tail per message, every
check site branching to it. PR #9520.

### `FERN_IR_VERIFY=1` did not run on the SSA emit paths

`buildArm64SSA` and `buildX86SSA` never called `ir.VerifyOrRefuse`, so the
gate that catches malformed IR before a backend consumes it (#8798) was silent
on both — no refusal, and no coverage line to say it had looked at nothing.
PR #9516.

### Two programs that compiled stopped compiling

```
arm64/ssa: arm64ssa: 2 call target(s) the module never defines
  — a runtime helper this backend does not emit: fn_termios_get, fn_termios_set
```

`TestArm64Termios` and `TestArm64HandleTty`. The refusal is loud, which is
what `-backend ssa` promises for an op outside its coverage — but as an
inherited default it is a build that used to work and no longer does. PR
#9524.

## Why the corpus was blind to all of it

A differential corpus answers one question: do the two backends compute the
same result for these programs. It is silent about

- a **runtime helper** no corpus program calls,
- a **diagnostic** no corpus program triggers (every one is expected to
  succeed, so none of them aborts),
- an **environment variable** no corpus harness sets.

## Enumerating was harder than it looks

After round 3 the lesson looked obvious: stop guessing, enumerate. So the gap
was enumerated by compiling every program under `coreutils/` and `examples/`
for arm64-linux and reading off what `checkNoDanglingCalls` refused. That
found the five of round 4 — and still missed `timer_fd`, whose only callers
are fixtures inside `internal/e2e`.

Replacing "guess the gap" with "enumerate two directories" was still a guess,
about where the callers live.

A structural gate was considered and does not work in the obvious shape:

```
providedSigs names:                374
arm64ssa emitters:                 200
in providedSigs but not emitted:   188
```

Almost all 188 are spelling variants of names the backend does serve
(`__fern_args` against `args`), wasm-only entries, or helpers it inlines
rather than calls. The faithful universe is "what the arm64 lowering emits
calls to", and the only honest way to enumerate that is to compile programs —
so the question is which corpus, and what it costs.

The e2e fixtures are the interesting answer: they already exercise all ten,
and miss them only because their arm64 leg runs on an aarch64 host alone.
Building them for both native targets on any host would have caught every
one, and costs nothing new to run. That is a change to how the fixture
harness picks targets, so it is proposed rather than done.

## What two of the fixes paid back

Routing the abort sites through one shared tail per message made a check site
one instruction instead of three. On the self-host driver, 4,612 sites:

| target | R+X before | after | |
| --- | ---: | ---: | ---: |
| arm64-linux | 8,758,660 | 8,721,116 | **−37,544** |
| x86-64-linux | 8,532,204 | 8,500,564 | **−31,640** |

The verify gate is byte-neutral: the driver is identical before and after on
both targets, which is the assertion that the restructure moved where the
live-set filter happens and not what survives it.

A note on measuring: the arm64 driver's **file size did not move at all**
across either change, because the image is padded to the same total. Reading
`ls -l` would have said both changes were worth nothing and one of them worth
less than nothing. The R+X `PT_LOAD` segment is the number that means
something here.

## The pattern, stated once

Every miss this round was one shape: **a gate that says "the default", "the
host", or "the programs I happened to look at" stops testing what you think
the moment any of those moves.**

- the corpus differential — the programs I happened to have;
- `TestEveryUserFunctionHasAnFDE` — names `-g`, which the SSA path refuses;
- `TestArm64Termios` and `TestArm64HandleTty` — inherit the default, so after
  the flip two legs ran one emitter and the stack machine ran none;
- `TestSelfHostSSABackendAgreesWithStackMachine` — takes its target from the
  host, so each machine tests one native and neither tests both;
- `TestX86_64SSASlices` — asserted an aborting program writes nothing, which
  was true only while the abort said nothing.

The fix is the same in every case: name the thing under test rather than
inherit it, and when a default moves, enumerate the flows the new path does
not reach instead of running the suites whose names match the feature.
