# 2026-09-05 — `Writer.write` is a copying builtin (#8394)

Native `internal/ir`, `copyingBuiltinArgs` in `rc_analysis.go`. Part of the
coreutils work (#8278): a `cat`-shaped loop that builds each output chunk in a
string accumulator and hands it to `w.write(out)`.

## The shape

```fern
match (r.read_chunk(131072)) {
    Some(chunk) => {
        var out: string = "";
        …
        while (i < n) { … out = out + piece; … }
        match (w.write(out)) { Some(_) => { return 1; }, None => {} }
    },
    None => { break; }
}
```

On the single-word string ABI `computeFreeEligible` taints a string local
passed to a call it cannot see through — "the callee may retain it, a leak at
worst" — and `copyingBuiltinArgs` is the exemption for the builtins that only
write the bytes out. The bare `write` builtin was in the table; the method
form `__method_Writer_write` (string at argument index 1, after the receiver)
was not. So `out` was borrow-tainted, `isSelfStrAppendLocal` refused the
in-place `__fern_str_append` path, every `out = out + piece` was a full
`OpStrConcat` copy of the growing accumulator, and the superseded copy
reached the flat `__fern_rc_dec` — decremented, never freed.

The entry is sound under the table's own rule (read the runtime body, not the
name): `__fern_writer_write` on both native backends loops `write(2)` over the
bytes and allocates a fresh `Option` box; nothing retains the string, and
`__method_Writer_write` was already in the inert rc-signature registry that
`TestCopyingBuiltinArgsAreInertPerTheRegistry` pins the table against.

## Measured — x86-64, `bin/fern -O`, 20 MB of ~44-byte lines

| program | before | after |
|---|---|---|
| c1 (accumulator in the arm, `w.write(out)`) | **OOM-killed at 72 s, 13.8 GB RSS** | 1.16 s |
| c2 (same loop in a helper, `w.write(build(chunk))`) | 1.18 s | 1.17 s |

After the fix c1 and c2 are the same program to the analysis; both are then
bounded by `__fern_str_append`'s class-step re-copy, which is the next entry.

## Traps

- **The trap was not the block scope.** The rc-plan dump (`RcPlanHook`) for
  the arm shape read `freeEligible: piece,r,w` with the write present and
  gained `out` the moment `w.write(out)` became `return out.len()`; a
  block-scoped `var`, a match binding feeding `slice_unchecked`, and the `str`
  view were each cleared by a variant before the call was suspected. Dump
  the plan before reading the analysis.
- **The two-word ABIs never had the taint**, so arm64 and wasm read this
  shape as clean throughout; only the x86-64 single-word ABI paid, and only
  a native x86-64 timing could see it.
- **The census pin for the new fixture is 4, not 0**, and none of the four is
  the accumulator: two `stdout()` handles and two `Writer.write` result
  boxes, sentinel-headered / header-less by design (`rcresults.go`, #8398).

## The self-host half: no copying-builtin credit at all

The three self-host fixture legs read `grows` on the new case, and the
variants say why: under `bin/fern-selfhost` the accumulator leaks with
`print(out)`, `strbuf_append(out)` and `__memchr(out, 10, 0)` exactly as with
`w.write(out)`, and is clean only with `out.len()`. `expr_unsafe_for_vb`'s Call
arm admits a bare-ident argument only at a borrowable position of a
user-declared function — the registry `borrowable_params_of` /
`borrowable_params_interproc` build from `FuncDecl`s — and a builtin has no
`FuncDecl`, so every builtin argument was an escape and the local lost its
release. The port is `copying_builtin_keys` (irlower.fern): the same names as
native's table, seeded into both registries as rows with position 0
borrowable (a constant tail of the interproc `rows`, so a rebuild keeps
them). The method form is admitted by NAME — the walk cannot see the
receiver's type — so `"Writer.write"` is seeded only while no user receiver
method is called `write`, and the walk maps a bare-ident receiver's `.write`
to that key.

`TestSelfHostCopyingBuiltinArgX86_64`: `print` / `eprint` / `__memchr` /
`__count_byte` / `Writer.write` free all 150 accumulator blocks over 50
rounds; a user `Sink.write` that retains its argument still reads the value
back. Two rows pin one extra unpaired block per call that the credit never
touched — `print`'s newline-joined temp and `Writer.write`'s result box, both
leaking identically with a literal argument (#8410).

## Pinned

`TestCopyingBuiltinArgIsCounted` (callee side, `writer-write` row),
`TestAccumulatorWrittenToWriterStaysInPlace` (caller side: `freeEligible`
carries `out` and the arm calls `__fern_str_append` exactly once),
`conformance/cases/alloc_flat_writer_accumulator` (AL-01: doubling the round
count is `flat` on x86-64, arm64 and wasm; `grows` on x86-64 without the
entry); `TestFernFixturesSelfHost{X86_64,Arm64,Wasm}` on the same case.
