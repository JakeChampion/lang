# Measured 2026-09-16: the self-hosted driver built through x86-64 SSA

`docs/ssa-log/2026-09-16-full-sweep.md` named the self-hosted compiler
built through SSA as the input the default flip still lacked. Building it
(`fern -target x86-64-linux -backend ssa examples/self_host/asm_ir_run.fern`)
found two things before it produced a binary.

**Three helpers had no emitter.** The build was refused with `strbuf_append`,
`strbuf_reset` and `strbuf_take` undefined: the global string builder the
self-hosted compiler writes its output through, which no `examples/**`
program uses, so the corpus differential never asked for it. They are
emitted now, on a buffer that grows through `__alloc` (doubling, the
outgrown block freed) rather than the fixed 64 MiB `.bss` the arm64 helpers
carry with no bounds check.

**The assembler, not the SSA layer, then took hours.** With the helpers in,
the build ran 26 minutes at a flat 212 MB before a goroutine dump placed it
in `relaxOnce`'s branch relaxation: with alignment pads in the text it
pinned one out-of-range branch per pass and re-laid out the whole text
between passes, and the SSA backend aligns every function with `.p2align 4`
where the flat backend emits no pads at all. #9446 settles relaxation in
one in-order pass, as GNU as does.

With both, on the 4-core container, best of one (the builds are minutes):

| build | compile | binary |
| --- | --- | --- |
| flat x86-64 | 18.5 s | 8.80 MB |
| x86-64 SSA | 91.7 s | 9.36 MB |

The SSA-built driver then compiles `lexer.fern` (`-ir`, 392 KB of assembly)
in 0.119 s against the flat-built driver's 0.139 s, best of three, with
byte-identical output; `parser.fern` and `checker.fern` cannot be compiled
by this driver on their own (the IR path bails on them outside the
per-module import set) and take 1.91 s / 1.12 s against 1.86 s / 1.23 s to
say so, identically.

**What this leaves.** The binary the SSA backend builds runs the compiler's
own workload at least as fast as the flat one on the module it can compile
standalone, with the same output. Two numbers now count against the flip:
the SSA path takes five times as long to compile the driver, and its binary
is 6% larger where the benchmark programs were smaller; both want a profile
of the SSA pipeline on this program before the flip is proposed.
