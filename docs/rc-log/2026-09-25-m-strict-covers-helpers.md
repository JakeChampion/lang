# Strict mode covers the runtime helpers

`FERN_SEM_IR_STRICT=1` failed a compile only when a program's own module kept
the AST lowering. A runtime helper (`asmcore.rt_src_*`) that the typed path
refused still fell back without saying so: `semlower.runtime_bodies` returned no
bodies, and `emit_ir_runtime_fern_fn` lowered the source through `irlower`.
Under strict, a refused helper now exits 3 after its `FERN_SEM_IR: runtime …`
line, whether it did not type-check, instantiated a generic, or was not
produced whole.

## What it found

The first strict sweep over coreutils found nine helpers, from four sources,
that had been counted as retyped but did not type-check. Every program calling
them took the AST lowering:

- `__fern_monotonic_ns`, `__fern_now_unix_ms` and `__fern_now_ns`: the shared
  `clock_read` still held the scratch block in an `i32` and passed it to
  `__syscall3` unconverted. It is a `usize` now, passed `as i64`.
- `__fern_geteuid`, `__fern_getegid`, `__fern_getuid` and `__fern_getgid`
  (one source, `rt_src_getid`) returned the `i64` syscall word from an `i32`
  function on x86-64 and arm64-linux. It narrows with `as i32`, as
  `cpu_count` does.
- `__fern_termios_get` and `__fern_termios_set` (`stty`) call
  `__fern_io_error`, but each was emitted as a source of its own, so the typed
  prune refused the call as having no contract. They join the fs bundle on
  x86-64 and arm64, beside `window_size`. The comment keeping them apart said
  only a separate emit marks the runtime an appending body needs; the bundle
  goes through the same `emit_ir_runtime_fern_fn`, and `read_dir` already
  appends inside it.

Past those, the sweep refuses nothing: the x86-64 conformance corpus, the
programs under `examples/` and all 106 coreutils on x86-64 and arm64 compile
under strict.

## Tests

`TestSelfHostSemIRStrict` compiles a program that calls the clock, id and
`termios_get` helpers under strict for x86-64, arm64-linux and arm64-darwin.
With the `geteuid` fix reverted it fails on x86-64 and arm64-linux, naming
`__fern_geteuid`.
