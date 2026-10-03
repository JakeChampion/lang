# The string-equality helper builds no frame

`__fn___fern_str_eq` is the hand-written helper the register path calls once
two strings' lengths compare equal. On both native targets it built a frame
on entry and copied its two operands into other registers before the first
compare. On x86-64 the call site also routed both operands through `%rdx` and
`%rcx` on their way to `%rax` and `%rsi`: four moves where two at most were
needed.

The frame had been kept so that `__fern_report` could walk the frame-pointer
chain for a backtrace. The self-host backends emit no such walker
(`asmcore.sanitize_on` lists the missing backtrace as a declared gap). A
walker added later would attribute the frameless helper's time to its caller
and miss its return address. Now:

- On both targets the helper's register entry compares the boxes where they
  arrive (`%rax`/`%rsi`, `x0`/`x9`) and returns with no frame.
- The x86-64 call site moves each operand straight into `%rax` or `%rsi`.
  Equality is symmetric, so an operand already in either register stays, and
  each move is skipped when its operand is already there.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container, stage 2
built from main at be489e35 and from this change on it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.444 G | 25.266 G (−0.70%) |
| `__fn___fern_str_eq.r`, self Ir | 417 M | 310 M |

Emitted bytes change: 520 of the 1,965 rows of the `selfhost-emit-hashes`
sweep differ from main (260 x86-64, 260 arm64), with the same 252 refused.
The arm64 helper loses the same frame and its two copies. It was not
measured: there is no instruction counter under qemu here.

## Witnessed

`TestSelfHostSSAStrEqComparesLengthsInline` now also fails on a helper that
builds a frame, on either target, and on an x86-64 call site that routes the
operands through `%rdx`/`%rcx`. All three assertions fail on main. The whole
`internal/e2eselfhost` package (4,866 passed) apart from two
`TestSelfHostSemanticProduction` cases that fail identically on main (#11169,
fixed by #11173); `TestFernFixturesSelfHostX86_64` (1,114 passed); the stage-2
build and its compile of `checker.fern`; the lint ratchet and `make fmt-check`.
