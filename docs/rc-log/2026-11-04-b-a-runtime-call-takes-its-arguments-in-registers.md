# 2026-11-04 — a runtime call takes its arguments in registers

A coreutils lead in #8278, from the `ls` rows of 4000 names.

## The shape

The sort in `ls` compares two names with `__fern_str_cmp` about 42,000
times. Each call pushed both strings, called the stack entry, and popped
them again. The stack entry then loaded both strings back from the stack
into the argument registers.

`__fern_str_cmp` is compiled from Fern, so it has a `.r` entry that takes
its arguments in registers, as a call to a user function does.
`ssa_str_call` pushed the operands of every runtime call. It did not
separate the runtime functions written in Fern from the ones written in
assembly, which have no `.r` entry.

## The rule

- `ssa_register_runtime` accepts a runtime call when the runtime bundle
  names its target for this build, the call has at most as many arguments
  as the argument registers, and each argument has a home.
- An accepted call moves its arguments into the argument registers in one
  parallel move and calls the `.r` entry. Other calls push as before.
- Both native backends use the rule. The arm64 backend reads the bundle for
  `s.runtime_target`, the same key that loads its runtime.

| x86-64, `ls` of 4000 names | before | after | GNU 9.4 |
|---|--:|--:|--:|
| whole run | 11,822,097 | 11,688,613 | 11,298,193 |

The output of `ls` is the same as GNU's.

## Tests

`TestSelfHostSSARuntimeRegisterCalls` compiles a program that compares
strings, for x86-64 and for arm64. Each call to `__fern_str_cmp` must reach
its `.r` entry, and no call can reach its stack entry.
