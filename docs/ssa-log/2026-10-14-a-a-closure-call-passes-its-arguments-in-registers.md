# 2026-10-14 — a closure call passes its arguments in registers

x86-64 only. `asm_ir.ssa_call_ind`, `ssa_entry_shim`, `ssa_addr_inst` (kind
45), `asmcore.const_agg_pack` and the `.globl` / `.weak` of each definition.
Refs #8171.

## The shape

A call through a function value pushed every argument, called the callee's
stack entry and popped the arguments off again. The stack entry loaded the
first ssa_nregs() of them back into the pool registers and fell through into
`.r`, the register entry a direct call in the same unit enters. On a stage-2
compile of `checker.fern` that was 19.6 M indirect calls and 50 M pushes,
most of them the AST walks' visitors and the map helpers' hash and equality
functions.

## What changed

A function value names its function's `.r` entry. Kind 45 and the static
closure boxes `const_agg_pack` packs both spell it, with the suffix the
backend passes. Every function now has a `.r` label: one with no parameters
has both labels at one address. Each `.r` takes the linkage of its function,
`.globl` or `.weak`, so a closure built in one module can name a function
defined in another.

`ssa_call_ind` then calls as a register call does:
- The target is read into `%rdx` first. That is neither a pool register nor
  a scratch the argument moves use.
- The first ssa_nregs() arguments go into the pool registers by parallel move.
- A call with more arguments also pushes all of them, first on top, which is
  where `.r` reads the ones past the pool.

arm64 is unchanged. Its `.r` entry places each parameter by the callee's float
mask, which a call site through a function value does not have.

## Measured

The stage-2 compile of `checker.fern` under callgrind, without `-g`, warm
certifier memo, both compilers built from the same base:

| | Ir |
|---|--:|
| slot sharing on bitsets | 14.185 G |
| closure calls in registers | 14.117 G (−0.48%) |

The stage-2 compiler recompiles itself byte for byte.

`scripts/perf-bench` x86-64 re-banks three rows:

| row | change |
|---|--:|
| `closure_call.ir` | −15.36% |
| `map_int.ir` | −2.94% |
| `map_probe_chain.ir` | −2.31% |

## Witnessed

`TestSelfHostClosureCallInRegisters` requires:
- a two-argument closure call to push nothing and call through `%rdx`;
- an eight-argument one to push all eight;
- the closures' values to name their `.r` entries;
- the program to exit as the interpreter does on every host target.
