# A lent array is bracketed only where the callee grows it

2026-09-24. Self-host, semantic lowering.

```
function scan_width(s: string, other: u8[]): i32 {
    ...
    while (i < n) {
        var j: i32 = __scan_set(s, i, other);
        ...
    }
}
```

Every turn of this loop retained `other` before the scan and released it
after, through a call to `__fern_arr_dec`.

## Cause

`ssarc.bracketed` holds a second count on a borrowed array across a call
the frame outlives, so that a callee growing the array in place takes the
copy instead. It did so for every borrowed array argument, whatever the
callee. That included builtins, which never grow what they are lent, and
user functions with no append on the parameter. A record argument was
already bracketed only at the fields the callee's grow row names.

An array is now bracketed only where the callee's grow row has the
parameter's own buffer (`ssaunits.grows_buffer`, field -1). The table
answers for every callee a kept body names: `semlower.prune` drops a body
that calls anything the AST lowering defines. The AST lowering already
brackets only at its may-grow positions (`callee_param_may_grow`).

## Measured

`wc -L` over a 62 MiB file of `seq` output, self-host build, x86-64:
139.5 ms to 110.3 ms (native build: 117.4 ms, GNU 9.12: 87.8 ms).
Instructions over the first 8 MB: 152.9 M to 123.3 M. `sort -m`, whose
arrays are owned locals, executes the same instruction count either way.

`TestSelfHostLentArrayBracket` checks both ISAs: the scan loop carries
neither half of the bracket, and a loop calling a function that appends
to its parameter keeps it, with an exit code showing the caller's array
was not grown.

## Trap

The `asm_ir_run.fern` driver the shape harnesses use never runs the
semantic lowering, so a shape test of an `ssarc` decision written on
`runX86ShapeCases` passes before the fix too. Go through the CLI
(`selfHostCLIForHost`).
