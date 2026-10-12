# 2026-10-12 — a kept field keeps only the construction's retain

Fixes #12135. `ssarc.block_body`.

## The shape

```fern
while (i < n) {
    let st: St = ctx.st;
    let d: string[] = ctx.diags;
    let w: Wrapped = wrap(st, i);     // wrap's parameter is counted
    ctx = Ctx { st: st, diags: d.append(w.n.to_string()) };
    i = i + 1;
}
```

`ctx` dies after its reads, so its box is the token for the `Ctx` built from
it. The construction keeps `st` in place: its retain of `st` is the one the
slot already holds, so on the unique path it is not emitted. The plan also
asks for a retain of `st` at the call, because `wrap` takes a unit of it and
`st` is read again afterwards.

`supplies` emitted every retain of a kept value as `unless_unique`, at every
step, not only at the construction. On the unique path the call's retain was
dropped too, so `wrap` took the slot's unit, `Wrapped` released it, and the
rebuilt `Ctx` held a freed `St`. The sanitizer reports the use-after-free when
`Wrapped` is dropped.

## The rule

Keeping a field in place stands in for the construction's retain of it and
nothing else. `block_body` hands `supplies` the values kept by the
construction at the current position, `Keeps.values[kb]`, and every other
retain of a kept value is an ordinary one.

## Why it took a rewrite to see

The issue found it through a local rewrite in `semsource.define` that hands
back a block's existing read of a record slot instead of reading it again. In
`asmcore.check_stmts`, `display_arg_errs(e.value, want, ctx.st)` and the
`CheckCtx` rebuilt around `ctx.st` read the slot twice, so the call's operand
was a value no construction kept. With one read, the call and the
construction share it, and the keep swallowed the call's retain of the
`EmitState`, whose schema tables hold the `TypeRef` arrays the sanitizer saw
freed. The same single read is plain source, which is the test.

Reproduce it from a checkout without `bin/fern-selfhost`. `bin/fern` runs that
binary in preference to building the embedded sources, so a stale one compiles
stage 1's own code without the change under test, and the crash goes away. A
bisect over main named a docs-only commit as the fix. Disabling the reuse
layer for one bucket of function names at a time, under the sanitizer,
narrowed it to `check_stmts` and then to its block 5.

## Witnessed

`TestX86_64KeptFieldCallSanitized` (and the arm64 and wasm legs) run the loop
above under the sanitizer and the leak census. Without the fix the x86-64 leg
fails with `fern-sanitizer: use-after-free`; with it the census balances.
With the issue's `define` rewrite applied on top of the fix, the sanitizer
stage 1 compiles `checker.fern` cleanly from the path that crashed, and the
plain stage 1 does so three times out of three.

## What is left

The default-on certifier passed the broken function: the retain under the
token's test and the consume after the join disagree at the join, which it
counts as unproved rather than a finding (#12181).
