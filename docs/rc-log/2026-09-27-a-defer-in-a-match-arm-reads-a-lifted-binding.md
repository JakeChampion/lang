# A defer in a match arm reads a lifted pattern binding

The last shape of #10323: a `defer` inside a match arm inside a loop, whose
action names the arm's pattern binding.

```
while (i < 3) {
    match (o) { Some(v) => { defer a.set(a.get() * 10 + v + 1); }, None => { } }
    i = i + 1;
}
return a.get();
```

The defer desugar arms a flag in the arm and replays the action behind it on
every edge out of the iteration, and again at every `return` of the function.
Both replays sit after the arm has closed. For a `let` the action names, the
desugar lifts the declaration to the top of the function at the zero of its
written type. It cannot lift a pattern binding: it runs before the checker,
and the binding has no written type. The typed path refused the function
with "unbound name is not a semantic value: `$binding$3$v`" under
`FERN_SEM_IR_STRICT`; without it, the CLI fell back to the AST lowering.

The typed path now does the lift itself. `semsource.escaping_bindings` finds
the pattern bindings used outside the arm that binds them. Lexical resolution
has given every binding its own symbol, so such a use can only be a replay.
`build` lowers the body once as a probe. At each match, `learn_escaping`
records the payload type of each escaping binding the arms bind. The probe
stops at its first refusal, which is past the match that showed the type, so
the probe repeats until no new type appears. The real lowering then binds
each learned binding right after the parameters, at the zero of its type. The
arm replaces the slot instead of binding a new one, and the loop and match
joins carry it through their phis. Functions with no escaping binding take no
probe.

`TestSelfHostDeferInLoopIR` moves to the CLI and passes all its cases on
x86-64 and wasm. It gains a `string`-payload case: exit 33, as native gives,
with the leakcheck census balanced (4 allocations, 4 frees).
