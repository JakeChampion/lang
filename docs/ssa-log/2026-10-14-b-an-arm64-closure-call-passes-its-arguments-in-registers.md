# 2026-10-14 — an arm64 closure call passes its arguments in registers

arm64. `asm_arm64_ir.ssa_call_ind`, `ssa_entry_shim`, `ssa_addr_inst` (kind
45), the static closure boxes and the `.globl` / `.weak` of each definition.
Refs #8171.

## The shape

x86-64's closure calls already pass their arguments in registers
(`2026-10-14-a`). On arm64 a call through a function value still pushed each
argument into a 16-byte slot, called the callee's stack entry, which loaded
them back, and popped the slots.

arm64's register entry `.r` cannot simply be named instead. It takes an f64
parameter in a float register, by the callee's float mask. A call through a
function value has no mask to read, only the value's static type, and a
function value need not have exactly that type.

## What changed

Each function now has a third entry, `.k`, between the stack entry and `.r`:
- The stack entry loads its arguments into the integer pool in order and falls
  into `.k`.
- `.k` moves each f64 parameter into the float register `.r` reads it from and
  falls into `.r`.
- A function with no f64 parameter has `.k` and `.r` at one address, and one
  with no parameters all three.

A function value names `.k`, as kind 45 and in the static closure boxes, and
`.k` takes its function's linkage. On Mach-O `darwinize` makes `.k` a local
label, as it does `.r`, and drops its `.globl` / `.weak`: as a symbol inside
the function it would split the function's CFI, and a Mach-O build is one
unit, so nothing outside the file names it.

`ssa_call_ind` reads the target into x6, which is neither a pool register nor
a scratch the moves use, and moves up to eight arguments into the pool in
order. Past eight it pushes every argument and loads the first eight back
from the pushes, since a spilled operand's slot is addressed from sp.

## Witnessed

`TestSelfHostClosureCallInRegisters` now also requires, on arm64:
- `apply` and `seven` to call through x6 with no argument pushed;
- the closures' values to name their `.k` entries, local labels in the
  Mach-O listing.

It also runs a program passing f64 arguments between integer ones, through a
call within the pool and one past it on both targets. The program exits as the
interpreter does.
