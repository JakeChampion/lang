# Store resolved coverage exits as numeric source positions

Part of #11534 and #8171.

A coverage build emits its final report before a call to the builtin `exit`.
A parameter, local or module function can have the same name.
The checker identifies builtin calls on the lexically resolved function.
Coverage instruments the original function, so the two passes need a common way to identify a call.

`ExitCalls` now stores each call's full line and column in one 64-bit integer.
Ownership checks use the same representation for field selections.
The checker sorts the positions once per function, and coverage searches them by binary search.
The line retains the imported file's encoded position.
The builtin flag still identifies the exits that the parser generates for `assert` and `todo` at position zero.

## Stage-2 measurements

Both compilers used the repository's pinned stage 0 followed by a self-compile.
Callgrind counted the instructions to compile the same source on x86-64 Linux with IR verification and ownership certification enabled.
Each row below uses `-cover` and produced byte-identical binaries.

| Covered source | Main at `349ae0674d1f` | Branch | Instructions saved |
| --- | ---: | ---: | ---: |
| One direct builtin exit | 79,225,478 | 79,223,922 | 1,556 |
| One conditional exit | 81,218,160 | 81,216,604 | 1,556 |
| 32 conditional exits | 110,341,331 | 110,328,740 | 12,591 |
| 128 conditional exits | 209,033,368 | 208,999,732 | 33,636 |
| No exits | 79,689,374 | 79,688,964 | 410 |
| Module function named `exit` | 79,689,404 | 79,688,960 | 444 |
| Builtin exit in an imported function | 80,107,028 | 80,104,872 | 2,156 |

The conditional fixture defines `probe(n: i32)` with one `if (n == i) { exit(17); }` for each integer from zero to N minus one.
The entry point calls `probe(N - 1)`.
The 128-site program returned 17 and printed the same 386 coverage counters on both native targets.

With coverage off, the fixed checker compile used 94,963,762,683 instructions on main and 94,963,317,058 on the branch (445,625 fewer). Both compilers emitted the same checker binary, allocated and freed 74,368,666 objects, and reproduced their stage-2 executables.

| Covered source | Main allocations | Branch allocations |
| --- | ---: | ---: |
| One conditional exit | 207,774 | 207,769 |
| 32 conditional exits | 244,755 | 244,688 |
| 128 conditional exits | 359,009 | 358,750 |
| No exits | 205,578 | 205,578 |

The leak checker reported zero live bytes after each compile.

## Verification

The focused tests compared the entire report, process status and binary on x86-64 and ARM64 Linux.
The tests included imported exits and parameters or module functions named `exit`. One line contained both a local call and a builtin exit.
The four assertion and todo programs also passed on both targets.
The added regression checks that the local call returns and the builtin prints the report exactly once.
The WASI and interpreter coverage refusals matched.

A separate driver fed the checker out-of-order and duplicate call coordinates. Some had negative lines or columns.
Both versions recognized the same exit calls and rejected a different column or file line.
A generated builtin exit at position zero still matched in the absence of a recorded site.

The two conformance reports matched on all 6,258 outcomes. Both compilers emitted 4,458 identical binaries and refused the same 1,800 inputs. All 526 expected-error JSON diagnostics, six fixed checker binaries, the HTTP component and both freestanding assembly outputs matched. Both lint ratchets passed.

Both stage-1 compilers measured 58,371,508 shared-array bytes in 1,020,515 copies on the fixed main checker. On the changed checker, both measured 58,471,712 bytes in 1,021,764 copies. The branch pins the two values for its own source. The stage-2 compiler executable grew by 3,696 bytes.

Both versions refuted 5,347 planted imbalances, weakened 228 and missed 227. All 2,360 per-function mutant records matched. The WASI checker had 71 unproved functions on each version. The native driver had 720 on main and 720 on the branch. Both builds passed all five certifier and both shared-array baselines.
