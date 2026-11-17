# A guarded read tests the sign of its index

2026-11-17. All targets. Issue #15746. Follows
`../rc-log/2026-10-04-a-an-index-a-dominating-guard-bounds-is-read-unchecked.md`.

## The shape

```fern
@noinline
function spaces(s: string, from: i32): i32 {
  let i = from;
  while (i < s.len() && s[i] == b' ') { i += 1; }
  return i;
}
```

The guard proves `i < s.len()`. It does not prove `i >= 0`, because `from`
can be negative. `ssabounds` removes the check of a read only when it has
the two proofs. Thus the read kept the full check. The loop compared the index
with the length two times for each element:

```
.Lssa_spaces_5:
    cmpq 8(%rax), %rsi
    jae __fern_str_oob_abort.l0
    movq (%rax), %rdx
    movzbl (%rdx,%rsi), %r8d
    cmpq $32, %r8
    jne .Lssa_spaces_1
    addq $1, %rsi
    cmpq %rdi, %rsi
    jl .Lssa_spaces_5
```

The read is one checked operation, so it also loaded the data pointer of the
string in each pass.

## The change

A read has a third state, `ssasem.below_proven()`. `ssabounds.proof_of`
gives it to a checked read of an `i32` index when `below` holds and `nonneg`
does not. A later run of the pass can still prove the read completely.

`ssarc` lowers such a read as two operations:

- `ir.sign_only` of the read. It reads nothing. It aborts when bit 31 of the
  index is set, with the message and the status of the full check.
- The unchecked read.

The emitters know the flag value 3 where they know the check-only value 2.
x86-64 emits `testl` and `js` to the trap. arm64 emits `cmp w, #0` and the
trap guard. wasm emits `i32.lt_s` and the abort call.

The test reads the low half of the index. `ssa.zero_extended_indices` can
make the extension of an index a zero-extension. A 64-bit test then
accepts a negative index.

```
.Lssa_spaces_5:
    testl %esi, %esi
    js __fern_str_oob_abort.l0
    movzbl (%r8,%rsi), %r9d
    cmpq $32, %r9
    jne .Lssa_spaces_1
    addq $1, %rsi
    cmpq %rdi, %rsi
    jl .Lssa_spaces_5
```

The unchecked read is a raw load, so the data pointer of the string moves
out of the loop. The function keeps no frame.

## Measured

Instructions for each element of the loop, from the listings:

| Loop | Target | Before | After |
|---|---|---:|---:|
| `string` | x86-64 | 9, with 3 loads | 8, with 1 load |
| `i32[]` | x86-64 | 8, with 3 loads | 8, with 2 loads |
| `string` | arm64 | 10, with 3 loads | 8, with 1 load |

A program calls `spaces` on 1,000,000 spaces, 20 times. It calls the same
loop on an `i32[]` of 1,000,000 zeros, 20 times. Callgrind, x86-64: 354,497,681
instructions before and 334,497,891 after (-5.6%). That is one instruction
for each element of the string loop.

Processor time of that program, x86-64, 25 runs of each binary: 24.2 ms and
24.1 ms at the best run. The difference is less than the noise of the
machine. The loads that the change removes hit the first-level cache.

## Tests

- `TestSelfHostGuardLeavesASignTest` checks the listings of x86-64 and
  arm64: a sign test before the read, no second compare with the length, and
  no frame.
- `TestSelfHostGuardSignTestAborts` runs a negative start on x86-64, arm64
  and wasm32 for a string, an `i32[]`, an `i64[]`, a `[i32]` view and a
  `[u8]` view. Each case aborts with the message and the status of the full
  check.
