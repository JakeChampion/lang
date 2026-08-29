# The lift rewrites call sites by name, and the name has no scope

`try_lift_binding` param-lifts a capturing-or-not lambda bound to a var and only
ever called: the binding statement is dropped and every `f(args)` becomes a
direct call to a hoisted `__lam_N(args, caps…)`. The rewrite is
`subst_fcall_expr`, which matches an `ExprIdent` callee by NAME and recurses
into `if` / `while` / `for` / `match` bodies.

It has no notion of scope. So a nested `var f = <another lambda>` has its OWN
calls rewritten to the OUTER lambda's hoisted body.

## Measured

```fern
function apply(v: i64): i64 {
    var g: (i64) => i64 = (x: i64) => x * 2i64;
    var t: i64 = g(v);
    if (v > 0i64) {
        var g: (i32) => i32 = (y: i32) => y + 1;   // shadows
        t = t + (g(3) as i64);                     // ran the OUTER lambda
    }
    return t + g(v + 1i64);
}
function main(): i32 { return apply(20i64) as i32; }
```

| shape | interp | self-host, before | rename control | after |
|---|---|---|---|---|
| shadowed signature | 86 | **88** — x86-64 AND wasm | 86 | 86 |
| shadowed return | 55 | **64** | 55 | 55 |
| shadowed dyn positions | 37 | **134** | 37 | 37 |

The control renames the inner binding and changes nothing else.

The emitted asm is the unambiguous form. All three call sites in `apply`:

```
call __fn___lam_0
call __fn___lam_0
call __fn___lam_0
```

`__fn___lam_1` is emitted and never reached. `40 + 6 + 42 = 88` where the
program means `40 + 4 + 42 = 86` — the inner call ran `x * 2` instead of
`y + 1`.

## The fix, and why declining rather than a scope-aware walk

`local_decl_count(body, fname) != 1` declines the lift.

The substitution's precondition is that the name resolves the same way
everywhere it looks. Where it does not, both bindings lower as ordinary closures
with their own slots, which is correct — just not lifted. A scope-aware
`subst_fcall_expr` would keep the outer lift, but the leftover-`fname` guard at
the end of `try_lift_binding` would then have to become scope-aware too (it
scans `collect_idents` over the whole body), and that is a second walk to keep
in step with the first for an optimisation, not a correctness win.

## The trap, which is the part worth carrying

These three programs were written as probes for a DIFFERENT bug — #7253's
fn-VALUE sidecars, which are name-keyed side tables read with a forward scan
while the callee's slot comes from a backward one. They went red exactly as
predicted, with clean rename controls, on two backends.

Re-keying those sidecars changed **nothing**: all three still diverged, and the
emitted asm was **byte-identical**. That last fact is what exposed it. If the
inner slot had stopped resolving the outer's row, the emit had to move; it did
not, because the shadowed lambda never reached the sidecars at all — the lift
had already rewritten its call.

> **A probe that goes red is not evidence about the mechanism you are changing.**
> Make the change and check whether the emitted bytes move *at all*. Byte-identical
> output under a still-red probe means the probe is measuring something else.

This sits alongside the three instrument failures already on #7253 (a probe that
cannot reach the condition, a corpus that cannot express the shape, a family
that never fires). It is a fourth shape: the probe reaches a real defect, and
the defect is not the one being fixed. Only the null result distinguishes them,
and only if you look for it.

Rows: `fnValueWideSigCases` in
`internal/e2eselfhost/self_host_fn_value_wide_sig_wasm_ir_test.go`, each paired
with its rename control, on the wasm leg and the x86-64 leg added with them.
Two of the three are invisible to wasm alone — a wrong funcref type is loud
there (the validator refuses the module, or the dispatch traps), but these move
the ANSWER, and the register backends have no structural funcref check to raise.
