# Dropping inner i32 wraps saves almost nothing

No slice landed from this. It is here so nobody builds it twice.

Every i32 add, sub, mul and friend is followed by a sign-extension
(`movslq` / `sxtw`), since the register path keeps an i32 sign-extended in
its 64-bit register. When the only reader of a wrapped value is more
low-bits arithmetic (add, sub, mul, and, or, xor, a left shift's shifted
operand) whose result is wrapped again at the same width or narrower, the
inner wrap is dead: `p.x * p.x + p.y * p.y` needs one extension, not three.

`drop_inner_wraps` did exactly that between `prune_trivial_phis` and
`prune_dead` on both native emitters, and was measured 2026-09-23 against
the same tree without it:

| subject | sign-extensions | static instructions |
|---|---|---|
| `checker.fern`, x86-64 | 1,536 → 1,525 | 479,522 → 479,509 |
| `checker.fern`, arm64 | 1,541 → 1,530 | 459,556 → 459,523 |
| `examples/bench`, x86-64 | | 84,055 → 84,036 |

Almost every wrap in real code follows a single operation whose result
feeds a phi, a compare, a store or a call — a reader for which the
extension is the invariant, not waste. A shift's own extension is part of
its instruction sequence (`ir_bin_asm`), not a separate op, and is out of
reach of an SSA pass anyway.

What would remove the extensions is changing the invariant: hold an i32
with undefined upper bits, select the 32-bit instruction forms (`addl`,
`cmpl`, `w` registers), and extend only where a value is widened, used as
an index or address, or divided. That is a change to both emitters' whole
integer selection, and it is worth at most the ~0.3% of static
instructions the extensions are — well behind the stack-argument calling
convention (about 15%).
