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
The owner chose the direct implementation on 2026-10-29: the built-in `close` gets a consuming receiver in both checkers.
The user-defined cases above show the discipline that follows.
That discipline needed two language rules first, and both checkers now carry them.

## The defer rule and the field-move rule

A deferred consuming call runs at the function's exit, so it is the last use of the names it consumes.
The handle stays readable after the `defer` statement, and a second consuming use anywhere in the function is refused.
The rule admits a fresh local or an `own` parameter, mentioned once in the deferred call, outside a loop, and not captured by a closure.
A deferred mention holds its path until the exit: a bare mention of `q` holds every field, and `q.g` holds `g` alone.

A field moves out of a fresh local or an `own` parameter when it is passed to an `own` parameter at the path's last use.
Nothing after the call reads that path or uses the whole, and a sibling field stays owned.
Inside a struct literal based on the same name, the superseded-field rule decides instead.
A field of a borrowed parameter or receiver does not move, because the caller still holds the whole.

A local dies at a call when no path after the call reads it again.
The checker follows the statements after the call, through the blocks that hold it.
A `return` ends the path, and a `break` continues it after the loop it leaves.
A `continue`, or the end of a loop body, comes back to every use in that loop.

A loop's head runs again each round, and so does a loop inside a value block.
A local the loop body declares is fresh each round.
Both checkers compute the same answer: the Go side in `liveAfter` and the Fern side in `lu_live_after`.

A match on a local at its last use moves the payload into the arm, and each binding is a fresh local.
A match on a field path at the path's last use does the same.
A pattern binding of an owned scrutinee owns its payload, so a field of the payload moves out at the path's last use.
A pattern binding of a borrowed scrutinee lends its payload, and no field of it moves.

A for-each over an owned array binds each element owned.
The array is a fresh local or a field path at its last use, an `own` parameter, or a fresh call.
A for-each over a borrowed array lends each element.
A loop deeper than the binding of an owner runs a consuming call again on the same value, so the call is refused there.

| Pattern | Code | Case |
|---|---|---|
| A deferred consuming close, then a read of the handle | accepted | `handle_defer_closes_at_exit` |
| Both fields of a fresh local closed, and both fields of an `own` parameter closed | accepted | `handle_field_closes_from_local` |
| A close after a deferred close of the same handle | `E051` | `diag_e051_handle_closed_after_defer` |
| A field closed twice | `E051` | `diag_e051_handle_field_closed_twice` |
| A transfer of the whole after one of its fields moved out | `E051` | `diag_e051_handle_whole_used_after_field_close` |
| A deferred consuming close inside a loop | `E051` | `diag_e051_handle_deferred_close_in_loop` |
| An `own` parameter closed after a deferred close | `E050` | `diag_e050_owned_handle_consumed_after_defer` |
| A deferred consuming close of a handle a closure captures | `E051` | `diag_e051_handle_captured_then_deferred` |
| A close in a loop before a return, before a break, before a labelled break, and a close of a loop-local each round | accepted | `handle_closes_on_loop_exit` |
| A close before a `continue` | `E051` | `diag_e051_handle_closed_before_continue` |
| A close at the end of each round of a handle declared outside the loop | `E051` | `diag_e051_handle_closed_each_round` |
| A close in a loop's head | `E051` | `diag_e051_handle_closed_in_loop_head` |
| A match on a local at its last use, and on a field path, closes the payload | accepted | `handle_payload_closes_from_local_match` |
| A field of a pattern-bound payload closed, from an `own` parameter and from an `if let` on a fresh call | accepted | `handle_payload_field_closes` |
| Each element of a fresh local array, of an `own` array parameter, and of an array field of an `own` parameter, and a field of each element, closed in a for-each | accepted | `handle_elements_close_in_for_each` |
| A match that is not the local's last use, with the payload closed | `E051` | `diag_e051_handle_matched_after_payload_closed` |
| A field of a payload bound from a borrowed scrutinee closed | `E051` | `diag_e051_handle_payload_field_of_borrowed` |
| An element of a borrowed array closed in a for-each | `E051` | `diag_e051_handle_elements_closed_from_borrowed_array` |

Both checkers report the same code at the same position for each row.

## The shapes the standard library still uses

The consuming built-in close refuses these shapes until they change:

| Shape | Sites | Change |
|---|---|---|
| A close of a payload matched out of a stored array element | `__tail_drop` in `internal/stdlib/std/serve.fern.md` | take the element out of the array first |
| A close of each end of a `Pipe` held in a borrowed parameter | `__snapshot_pipe_close` and the supervisor spawn in `serve.fern.md`, `tests/stdlib/async_combinators_test.fern` | an `own` parameter |
| A close of a field of a borrowed receiver | `BufWriter.close` in `internal/stdlib/std/io_buffered.fern.md` | an `own` receiver |
| A close of a borrowed `Reader` parameter that another branch stores | `__range_tail` in `serve.fern.md` | an `own` parameter |

The rules above accept a close through `defer` (`examples/wasm/wc.fern`) and a close of each end of a `Pipe` held in a fresh local.
They also accept a close on a loop path that leaves the loop.
A payload a match or a for-each takes can close, and so can an element `array.swap_at` takes out of an array it owns.
A handle an `open_*` builtin returns borrows nothing from its path argument, so it is a fresh local.
The built-in signatures change in the next step, with those sites.

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
