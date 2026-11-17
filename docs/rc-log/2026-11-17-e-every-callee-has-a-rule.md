# Every callee has a rule, and a callee with none is a refusal

2026-11-17. x86-64, arm64 and wasm32, with a check on arm64-darwin and
x86-64-android. The change is in `semsource`, `semlower`, `rcsigs`,
`rccertify`, `ssasem` and `ssabytes`.

## The gap

A call to a callee with no rc signature and no ABI was opaque. The
certifier made each argument of such a call unproved and went on. Over the
2,749 programs under `conformance/`, `tests/`, `examples/` and the
utilities, the programs made 1,511 opaque calls on x86-64 before this change.
Most went to four kinds of callee:

- an instance of a generic function, for example `Option.unwrap_or$str`
- a routine of the task runtime, for example `__fern_task_free`
- a byte-view kernel, for example `__sem_byte_view_memchr`
- a send helper or its table of copied boxes, for example
  `__sem_sendpart_string` and `__sem_seen_get`.

## The change

- An instance gets a contract. `produce_module` keeps the contract of its
  declaration with the binding of the request that made it
  (`semsource.instance_contract`). The physical contracts add it with the
  modes that the inference gave the instance.

- A contract from the callers' calls was tried first and is wrong. The
  callers are already rewritten to take a pair return apart. Their calls
  give the first word of the result, an `i64` where the instance returns a
  `Seen { bytes: i64, label: string }`. The certifier then called the second
  word a leak, in `worker.__join_scheduled$Seen`.

- A runtime unit is lowered when a program first needs it, after the
  program's gate exists. So `rcsigs` gives the rules below.

- The task routines take and return words, and `__fern_task_wait` returns a
  fresh array. The byte-view kernels read their views and return a word. A send helper
  borrows its value and returns a fresh copy. `seen_get` returns a copy with
  a retain of its own, or null. `seen_put` passes the unit of its third
  argument to its result.

- `send_shared` tests the result of `seen_get` against a 64-bit zero. The
  certifier read only a 32-bit zero as zero, so it found a leak on the miss
  arm. A `CONST_I64` of `"0"` is now a zero (`rccertify.zero_word`).

- A call to a callee with no rule is a refusal with exit 5, and the census
  lane has no row for opaque calls.

## Measured after

The same 2,749 programs make 0 opaque calls and give 0 findings on
x86-64, arm64 and wasm32. 400 programs under `tests/stdlib/` and
`coreutils/` give the same on arm64-darwin and x86-64-android.

| target | unproved before | unproved after |
|---|---:|---:|
| x86-64 | 148,178 | 139,592 |
| arm64 | 145,013 | 136,488 |
| wasm32 | 112,567 | 106,302 |

The emitted code is the same: the assembly of `examples/age`, the worker test
and a conformance case is byte for byte the same before and after.

The census over `compiler/fern_main.fern` gives 17,404 unproved values,
against 17,399 for main. The subject is the compiler's own sources, so the 5
values are in the code that this change adds. Over the same sources, the
compiler before this change and the compiler after it give the same count for
each function.

## What is left

- The 59 functions that resume from a task frame are not certified.
- A callee with an unsure effect, such as `__fern_arr_concat`, has no model
  of "the argument or a new box".
