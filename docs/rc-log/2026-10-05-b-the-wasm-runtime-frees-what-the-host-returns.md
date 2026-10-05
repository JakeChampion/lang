# The wasm runtime frees what the host returns

2026-10-05: `wasm_ir.read_dir_func_like`, `build_io_error_p2_func`,
`clock_funcs_p2`, `env_func_p2`, `config_env_func_p2`, `args_func_p2`,
`environ_func_p2`, and the `@import` result wrappers in `extern_wrappers`,
`extern_sum_result_tail`, `extern_tuple_result_wrapper` and
`async_lower_wrapper`.

## What leaked

A sweep forced `FERN_LEAKCHECK` on every wasm program the self-host and e2e
suites build: 1,002 tests, 7,416 wasmtime runs, with the 335-fixture wasm
corpus and the differential leg. About 45 hand-written probes ran besides,
each as a core module and as a component. The typed lowering was clean. Every
leak was in a hand-written runtime body:

- **Core-module `read_dir` and `read_dir_all`:**
  - the 4096-byte `fd_readdir` buffer was never freed;
  - names were appended with `$__fern_arr_push`, so each array a push
    replaced stayed allocated;
  - an `fd_readdir` failure leaked the partial array.
- **Component I/O errors:** `$__fern_build_io_error_p2` retained the path
  its caller had already retained, so every `Err` from `read_file`,
  `read_file_bytes` or `write_file` kept one extra reference to the path.
- **Host results:** these bodies freed neither their return area nor the
  `cabi_realloc` blocks the host wrote:
  - `env`, `args`, `environ` and `config_get`: the host's list and every
    string in it;
  - the wall clocks: the return area only;
  - the `@import` result wrappers: their return areas, and the host's list
    or string for a list, string, string-variant or `list<tuple<string>>`
    result.

`env()` alone cost 2 + 2 × (the number of environment variables) blocks per
call, found or not.

## Measured

`FERN_LEAKCHECK`, live bytes at exit:

| Program | Before | After |
| --- | ---: | ---: |
| `read_dir` of 100 names, three calls, core module | 14,064 | 0 |
| A directory walk (`dirs`), core module | 135,696 | 0 |
| The same, component | 1,072 | 0 |
| `env` found, three calls, component | 360 | 0 |
| `args`, three calls, component | 120 | 0 |
| `now_ns` and `now_unix_ms`, three calls, component | 96 | 0 |
| A missing `read_file`, component | 208 | 0 |
| A list and a string from `get-random-bytes`, four calls | 256 | 0 |

Allocations and frees balance in every probe now, except one 256-byte block:
the fd table, which is allocated once and lives for the whole process.

## Witnessed

`TestSelfHostWasmHostResultsBalanceTheCensus` requires a balanced census, as a
core module and as a component, for:
- `read_dir` and `read_dir_all` over a name of every length from 1 to 33;
- a missing directory;
- `env` found and missing, with the clocks;
- `args`;
- `environ`;
- a string and a list returned through `@import`.

On main, six of its cases fail. `TestSelfHostWasmIoErrorOwnsPath` already
compiled its read and write cases as components and ran them. Its comment said
a component prints no census, so it never checked one. It does now, and those
three cases fail on main.

## Found on the way

- A return area has to start zeroed once it can be a recycled block.
  `std/wasi_http`'s `stream_read` reads its `stream-error` case as an `i32`,
  but the host writes only the discriminant byte. A fresh bump block had
  always been zero above it. A recycled one held the free list's link there,
  so every read ended as a failure: `TestSelfHostWasiHttpOutgoing` failed on
  the first version of this change.

- A component calling only `environ()` could not run. `host_needs`' `env`,
  which picks the component's framing from the parse tree, counted `env` and
  `config_get` but not `environ`, and the `cabi_realloc` export was gated on
  `env` and `args` alone. Both now include `environ`; the environment import
  itself already did.
- `std/async`'s `race` never closes a loser's socket: the 16-byte tcp record
  on wasm, a descriptor on native. A future has no cancellation hook to hand
  it; #11599.

## Next

The sweep's remaining non-zero censuses are structural:
- the fd table;
- `exit()` called below `main`;
- runs that end in a trap;
- the census's positive controls.
