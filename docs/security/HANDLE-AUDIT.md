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
Each one exposes its descriptor as the field `fd`, and `close` consumes its receiver.

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

## The consuming built-in close

Before 2026-10-29, both checkers accepted a program that used a built-in handle after its close.
The engines then disagreed.

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
The owner chose the direct implementation on 2026-10-29: the built-in `close` consumes its receiver in both checkers.
Both probes above are now refused at compile time for `Reader`, `Writer`, and `Dir`.
The `tcp_*` descriptors are plain `i32` values and still reach the hazard.

| Pattern | Code | Case |
|---|---|---|
| A second close of a built-in `Reader` | `E050` | `diag_e050_builtin_reader_closed_twice` |
| A write to a built-in `Writer` after its close | `E051` | `diag_e051_builtin_writer_written_after_close` |
| A function that returns `Some(h)` of a borrowed `h`, its result passed to an `own` parameter | `E051` | `diag_e051_variant_result_borrows_its_payload` |
| A local initialised from a call that returns its borrowed argument, passed to an `own` parameter | `E051` | `diag_e051_local_from_borrowing_call` |
| A local initialised from a call whose result cannot hold a handle, passed to an `own` parameter | accepted | `local_from_handle_free_call_is_fresh` |

The two `E051` rows above the last row close two holes that the consuming close exposed.
A variant constructor carries its payload, so its result borrows what the payload borrows.
A local initialised from a plain call is fresh only when the call's result borrows nothing at that site.
That rule applies only to a result that can hold a handle.
A number, a string, or an array of them cannot hold one, so two names for such a value cannot close a handle twice.
A match on a call follows the same rule as a local initialised from that call.

## Admitted locals

Both checkers admit a local before it passes to an `own` parameter at its last use.
A name with more than one declaration is a fresh local when each of its declarations is fresh.
An unpack still needs an owner with one declaration, because the checkers count the field reads by name.
A local set from an `own` parameter at the parameter's last use is admitted when it is declared once and outside a loop.
Each later assignment to that local must be a fresh value.
The cases are `own_arg_redeclared_fresh_local_and_owned_alias` and `diag_e051_stale_redeclaration_and_reassigned_alias`.

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

A call to a function that never returns ends the path.
A move on that path does not reach a later round of a loop.
It also does not reach a join after a branch.
A name a match arm binds is declared afresh each round of the loop that holds the match.
A call that moves a field can read other fields of the same owner in its other arguments.
The move takes only the one field.

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
| A close in a loop by a call to a function that never returns, and a read after it on another path | accepted | `handle_consumed_before_a_call_that_never_returns` |
| A payload taken in a loop from a field of an unpacked local | accepted | `handle_payload_closes_in_loop_from_unpack` |
| A field closed by a call that reads other fields of its owner | accepted | `handle_field_closes_beside_other_field_reads` |
| A field moved by a call that reads the same field in another argument | `E051` | `diag_e051_handle_field_moved_beside_same_field` |
| An own parameter closed each round of a loop | `E050` | `diag_e050_owned_handle_consumed_inside_loop` |

Both checkers report the same code at the same position for each row.

## How the code that closes handles changed

| Shape | Change | Sites |
|---|---|---|
| A helper that closes a handle it was given | the parameter is `own` | the range helpers and the snapshot pipe helpers in `std/serve`, `BufWriter.close`, and the coreutils reader, writer and output helpers |
| A close of a handle stored in an array | `swap_at` takes it out first | `__tail_drop` and `__tail_advance` in `std/serve`, du, paste, split, sort |
| A struct whose handle field closes while the struct stays in use | the update that replaces the field comes first, then the close | od, tail, split, csplit |
| A reader variable that a loop reopens | the variable takes the standard-input placeholder after its close | cut, expand, fold, unexpand, tail |

A handle an `open_*` builtin returns borrows nothing from its path argument, so it is a fresh local.
A directory opened under another directory (`Dir.open_dir`) is fresh the same way.

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
