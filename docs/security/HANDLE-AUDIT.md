# Resource handle audit

Status: [reference] probe record for [SEC-008](https://github.com/JakeChampion/lang/issues/13510).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

Each probe is a short program with one handle pattern.
Both checkers ran each probe.
A probe that both checkers accept also ran on the Go oracle, the self-host interpreter, native x86-64, and native arm64 under QEMU.
The probes that pin a rule live in the normative corpus as `RH-01` to `RH-05`.
The rest are in this record.

## The two handle kinds

A user-defined handle is a struct with a consuming close, written `function (own h: Handle) close()`.
The call-site rule `E051` admits a consuming call only on a fresh construction, on another `own` parameter, or on a local at its last use.
That rule gives the handle an affine discipline with no runtime cost.
A second close is refused at the first close.
A read after the close, and a use after a transfer to an `own` parameter, are refused the same way.

A built-in handle is a descriptor.
`Reader`, `Writer`, and `Dir` are handle types in the typed IR (`t_handle` in `compiler/semsource.fern`).
The reference-count lowering treats them as scalars (`semtypes.is_handle` in `compiler/ssarc.fern`).
Each one exposes its descriptor as the field `fd`, and `close` borrows its receiver.

The socket API (`tcp_listen`, `tcp_accept`, `tcp_close`, and the other `tcp_*` builtins) takes and returns a plain `i32`.
A `resource R` declaration is the WIT import surface only.
Its `own R` and `borrow R` types erase to `i32`, and no standard library type uses them.

## Refused by both checkers

Each row is a user-defined handle with a consuming close.
Both checkers report the same code at the same position.

| Pattern | Code | Case |
|---|---|---|
| A local closed twice | `E051` | `diag_e051_handle_closed_twice` |
| A field read after the close | `E051` | `diag_e051_handle_read_after_close` |
| A use after a transfer to an `own` parameter | `E051` | `diag_e051_handle_used_after_transfer` |
| `defer h.close()` before or after a read of `h` | `E051` | this record |
| A close of a payload matched out of a borrowed array element | `E051` | this record |
| A close of each of two fields of one local struct | `E051` | this record |
| A close of a field of a borrowed parameter | `E051` | this record |
| A close of a field of an `Option` payload from a borrowed parameter | `E051` | this record |
| A close after a closure captured the handle | `E051` | this record |

## Accepted by both checkers and sound on every engine

| Pattern | Case |
|---|---|
| A close at a local's last use, a transfer to an `own` sink, and an `own` parameter that one branch closes and the other branch stores | `handle_transfer_closes_once` |
| A second close of a built-in `Reader` with no open between the closes | `io_error_other_double_close_reader` |

The second case answers `Other("", "Bad file descriptor", 9)` on the Go oracle, the self-host interpreter, x86-64, and arm64.
The Wasm runner preopens no directory, so the case carries a harness-limit waiver.

## Divergence left for a language decision

Both checkers accept a program that uses a built-in handle after its close.
The engines then disagree.

| Probe | Go oracle | Self-host interpreter, x86-64, arm64 |
|---|---|---|
| Open A, close A, open B, read through A | the read is refused | the read returns the bytes of B |
| Open A, close A, open B, close A again, read B | the second close is refused and B stays readable | the second close succeeds and the read of B fails |

The native engines pass the descriptor to the kernel, and the kernel reused it for B.
The Go oracle refuses because its descriptor table never reuses a number.
Neither engine implements a closed state on the handle.
These two probes are the use-after-close and double-close hazards that SEC-008 names.
Safe code reaches them today through `Reader`, `Writer`, `Dir`, and every `tcp_*` descriptor.

The accepted design in `docs/SAFETY-CONTRACT.md` requires a refusal when a resource handle escapes after close or ownership transfer.
The direct implementation gives the built-in `close` a consuming receiver in both checkers.
The user-defined cases above show the discipline that follows.
That discipline refuses five shapes the standard library uses today:

| Shape | Sites |
|---|---|
| A close through `defer` | `examples/wasm/wc.fern` |
| A close of a payload matched out of a stored array element | `__tail_drop` in `internal/stdlib/std/serve.fern.md` |
| A close of each end of a `Pipe` held in a local or a borrowed parameter | `__snapshot_pipe_close`, `__snapshot_writer`, and the supervisor spawn in `serve.fern.md`, `tests/stdlib/async_combinators_test.fern` |
| A close of a field of a borrowed receiver | `BufWriter.close` in `internal/stdlib/std/io_buffered.fern.md` |
| A close of a borrowed `Reader` parameter that another branch stores | `__range_tail` in `serve.fern.md` |

A consuming built-in close therefore needs two language rules first.
The first rule says how `defer` discharges a consuming call.
The second rule says how a field moves out of a local or a borrowed container.
`docs/MUST-CONSUME.md` lists both as deferred.

This PR changes no built-in signature, and this record is the decision input.
The alternative is a runtime closed state on each handle, which needs a generation check in every handle helper on every target.
Neither alternative is in this PR.

## Commands

```
bin/fern -check PROBE.fern internal/stdlib
bin/fern-oracle -check PROBE.fern
bin/fern-oracle -interp PROBE.fern
bin/fern -interp PROBE.fern internal/stdlib
bin/fern -target x86-64-linux -o PROBE PROBE.fern internal/stdlib
bin/fern -target arm64-linux -o PROBE PROBE.fern internal/stdlib && qemu-aarch64 PROBE
```

Host: Linux 6.18.44-fc-v165, Intel(R) Xeon(R) Processor @ 2.80GHz, Go go1.27.3, QEMU 8.2.2, wasmtime 46.0.1.
