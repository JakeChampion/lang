# The retention was never the taint

#9542 reverted the arm64 SSA default over string retention and named the cause:
the single-word string ABI's #4174 reclaim taint. #9549 wrote that up with a
cost table. This round set out to fix the taint, fixed a real leak in it, and
found the attribution was wrong.

## What the taint actually refuses

`internal/ir/rc_analysis.go` taints a string local passed to a user function
out of reclaim unless the counted-retain summary clears the callee's position.
The summary, `stringParamCounted`, had arms for an assignment destination, a
consumed-threaded `return p`, `StructLit` / `TupleLit` / `ArrayLit` fields,
`Index`, `SliceExpr`, `Binary`, pure-read builtins, counted array stores, and
the argument-position rule. It had no arm for the plainest thing a callee can
do with a string parameter:

```fern
function tag(src: string, i: i32): i32 {
    var x: string = src;
    return x.len() + i;
}
```

That refused `src`, and the refusal reached every caller. Measured on 400
rounds under `FERN_LEAKCHECK`: **400 allocations, 0 frees**, where the same
body written `src.len()` freed all 400.

The binding retains nothing, and the lowering already knew it.
`TestBorrowedParamAliasTakesNoInc` pins that a local aliasing a borrowed string
parameter takes no reference of its own — the borrowed-alias cancellation
elides the transfer inc against an exit sweep that never touches the slot. So
the summary was strictly more conservative than the lowering it feeds. It has
to work that out separately because it runs in a whole-program fixpoint before
any builder exists, and the two escape checks the builder uses
(`bindingConfinedToArm`, `aliasReturnsConfined`) are builder methods.

`frameBoundStringAliases` closes it by collecting the locals that bind the
parameter — transitively, never reassigned, declared once, never mentioned
inside a `Lambda` — and classifying their occurrences with the arms that were
already there. An occurrence of the alias retains exactly what the same
occurrence of the parameter would, because it names the same buffer under the
same ownership.

Reassignment is deliberately excluded, and that is not an oversight: a
reassigned seed is `countedSeedOccurrences`' case and is credited on the
opposite grounds, because a rebindable binding DOES emit a real transfer inc.

## The shape that stays refused

`return x` on an alias is still refused while `return src` is credited. When
the alias escapes, the builder declines the cancellation and emits a real inc,
so crediting it would probably be sound — but "probably" is not the bar for a
change whose failure direction is a use-after-free, and the conditions that
settle it are builder state the summary runs too early to see. The refusal is a
leak, which is the safe direction. It is written down in the test rather than
left as an oversight.

## Then the measurement disagreed

The fix made no difference at all to the programs #9549 tabulated. Byte-identical
binaries. So the taint was not what those numbers measured.

`coreutils/uniq.fern` at 8,000 lines, at exit under `FERN_LEAKCHECK`:

| | allocs | frees | live_bytes |
| --- | ---: | ---: | ---: |
| x86-64 flat | 106 | 96 | 400 |
| arm64 flat | 118 | 108 | 368 |
| arm64 `-backend ssa` | 122 | 111 | 369,040 |

x86-64 runs the **same single-word ABI** — `ast.TwoWordOverride` is set only by
`internal/codegen/arm64` — so it carries the same taint, and it is clean. Three
orders of magnitude apart with the object counts a step apart: 10 unfreed
against 11. Never "more objects leaked". One object whose size tracks the input.

The reproducer has no aliasing and no user function in it at all:

```fern
function main(): i32 {
    var r: Reader = stdin();
    var n: i32 = 0;
    loop {
        match (r.read_line()) {
            Some(line) => { n = (n + line.len()) % 101; },
            None => { break; }
        }
    }
    return n % 7;
}
```

| input | arm64 flat | arm64 `-backend ssa` |
| --- | ---: | ---: |
| 1,000 lines | allocs=2002 frees=2001 live_bytes=**16** | allocs=2002 frees=2001 live_bytes=**1,520** |
| 8,000 lines | allocs=16002 frees=16001 live_bytes=**16** | allocs=16002 frees=16001 live_bytes=**12,752** |

Identical counts on both backends. One unfreed object either way: 16 bytes on
the stack machine, the grown read buffer on arm64ssa. Filed as #9558, which is
the actual precondition for any future SSA default.

## What went wrong in the diagnosis

Both facts were in front of me from the start — the taint is real, and the
coreutils retention is real — and I connected them because they were about the
same subsystem and pointed the same direction. What I never did was run the
control: x86-64 is single-word too, and one command would have shown it clean.
The ABI table in `docs/BACKEND-PARITY.md` said so in writing.

The alloc/free counts were also in the original write-up — "counts barely
differ" — and were read as "it is the buffers, not the objects", which is true,
and not as "then it is one buffer, so it is not a per-call taint", which is
what they actually say. The number that would have settled it was quoted and
not used.

`FERN_LEAKCHECK` cost a round too: it is read by the **compiler** at build
time, so the first measurements set it on the produced binary and reported
nothing at all.

## Landed

- `frameBoundStringAliases` in `internal/ir/rc_analysis.go`, and
  `stringParamCounted` classifying the alias set.
- `internal/ir/frame_bound_string_alias_test.go` — the credited and refused
  shapes, plus the direct-vs-alias agreement the change is really about.
- `internal/e2e/single_word_string_alias_reclaim_test.go` — the two spellings
  compiled and run as a pair on x86-64, where the single-word ABI is the
  default. Verified by mutation: reverted, it reports `frees 0 of 400` against
  400.
- The attribution corrected in `docs/BACKEND-PARITY.md`, `cmd/fern/main.go` and
  `internal/e2e/arm64_default_string_reclaim_test.go`, all three of which named
  the taint as the cause of the SSA retention.
- #9558 (the real cause) and #9559 (x86_64ssa cannot build the coreutils at
  all: a duplicate `.Lssa_mm_vec` label, and no `fn___method_Reader_stat`
  emitter — which is why there is no x86-64 SSA row in any of these tables).
