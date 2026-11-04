# 2026-11-04 — a call that never returns ends its block

Main was red in the Docs build. The TLS example in
`site/src/content/docs/http/protocols.md` failed in the certifier:

```
fetch____driven_kernel: spent without a unit of v433 (borrow ld_at) in b134: released by __fern_rc_dec
```

## The shape

The program serves over TLS and makes no HTTP/2 request, so no `h2.Conn`
is built. `semreach` thus finds that no return of `h2__Conn.read` is
reached, and the body of `h2__Conn.read` becomes a trap: `exit 134`, then a
zero result. `__driven_kernel` calls `d.h2.read(bytes)` and then splits the
`Step` result. After the trap was spliced into the caller, that code read
fields of the zero result and released them. That code cannot run, but the
certifier read it and refused the releases.

The fault was not new. The stdlib rewrite (`3e4bf0447`) changed
`return 0 - 1;` to `return -1;` in `__driven_at`. That changed what the
inliner kept, and `__driven_kernel` then stayed a function of its own.

## The rule

`semreach` now ends a block at a call that cannot continue:

- When no return of the callee is reached, the call runs and the rest of
  the block does not. The block keeps the call and ends in `unreachable`.
- When an argument holds no value, the call does not run. This is an
  argument whose value is `top`, or a record of a type that no reached
  code builds. The block ends before the call.

The walk records the cut for each block. `absorb` takes only the
instructions before the cut, so the callee of a pruned call is not reached.
A walk also records each record type whose absence cut a call. `absorb`
adds the function to the waiters of that type, and the function is walked
again when the type becomes inhabited. Without that walk, a cut stays after
its record is built: the stage1 compiler then miscompiled `tr`.

The pass does more work. When the compiler compiles `checker.fern`, it
makes 107,429 more allocations (0.28%), all in `semreach`: with
`FERN_NO_SEM_REACH=1`, main and this change both make 38,195,744. An
earlier form queued every function that passes a record. It cost 132,654
allocations.

In the TLS server, the call to `h2__Conn.read` is now pruned. Before this
change, the trap was spliced into its callers. The compiler that the
bootstrap builds is 1.2% smaller: 16,325,248 bytes against 16,529,072.

## Tests

- `TestSelfHostSemReachNoReturnCall`: a call to a function that only
  calls `exit`. The function that follows it in the caller is not emitted.
  With `FERN_NO_SEM_REACH=1` it is emitted.
- `TestSelfHostSemReachServeWithoutH2` now holds because the call is
  pruned. With only the first rule, `__fn_h2__Conn__read` stayed linked.
- `scripts/check-site-examples`: 92 programs, 0 failed.
