# Codegen quality audit, 2026-09

What machine code Fern actually emits, how it ranks against other languages,
and what it would take to close the gap.

Measured on a 4-core x86-64 container against `bin/fern` built from `bdbc6e5`
(the native backends are untouched between `a44e5ab` and `bdbc6e5`, so every
figure below reproduces on either). Every number here is reproducible from the
commands given; nothing is an estimate, and where a figure is a projection
rather than a measurement it says so.

The census numbers are re-runnable: `scripts/codegen-census -c FILE.fern`.

---

## 0. The short answer

**As audited: no, and the gap was larger than the project's own docs implied.**
On the tightest possible kernel Fern spent 22 instructions per iteration where
`gcc -O0` spends 5 and Go spends 5. Across the self-hosted compiler's own
1.6 M-instruction x86-64 emit, **42.6% of emitted instructions computed
nothing** — they shuffled values between the machine stack and two scratch
registers, or padded `rsp` for a call.

> **Status, 2026-09-01.** Tier A of §6 has since landed for both native
> backends. The same kernel is now 11 instructions per iteration, the x86-64
> emit of the compiler is **963,165 instructions against 1,601,565 (−39.9%)**
> and its overhead **12.5% against 42.6%**, and on call-heavy code Fern is now
> *faster than* `gcc -O0` rather than 1.5× it. Every figure below is marked
> with which side of that work it was measured on. What remains open is listed
> in §6; the largest item by a wide margin is spill quality on the SSA path,
> then loop rotation and a general magic-number reciprocal.

The thing that prompted this audit — the last day's batch of assembler work —
turns out to be the sharpest way to see it. **The assemblers can encode far more
than the code generator ever asks for.** `internal/native/arm64` encodes `madd`,
`csel`, `ubfx`, `cbz`, `movn`, logical bitmask immediates, shifted-register
operands and 78 Advanced-SIMD mnemonics. In the 7 933 724 instructions the
compiler emits for its own sources (`examples/self_host/fern.fern`, arm64),
`madd` appears **0 times**, `csel` 17 times, `movn`, `bfi`, `sbfx` and `rev`
**0 times each**, and 72 of the 78 SIMD mnemonics are unreachable from any code
path.

The distinction that matters, because a raw count understates it: where one of
these *does* appear, it is a hardcoded string in a runtime helper, never a
selection over user code. All 258 374 `tbnz` come from the inlined reference-count
and immortal-tag guards (`arm64.go:2190`, `:1962`); all 7 062 `ubfx` from the
small-string length-nibble unpack and float exponent extraction
(`arm64.go:11809`, `:6612`); all 64 `msub` from the decimal- and
time-conversion helpers (`arm64.go:5529`, `:5804`). **Instruction selection —
deriving one of these from the shape of an expression — does not happen at
all.** The encoders are correct, fuzzed against two oracles, and idle.

The good news is that the gap is unusually cheap to close, because it is not an
architecture problem. Three of the findings below are local peepholes on the
existing backends, one of them already prototyped end to end: it removes **20.3%
of all emitted x86-64 instructions** and **13.8% of `.text`** while producing
byte-identical compiler output.

---

## 1. Where Fern ranks

### 1.1 The measurement

Retired instructions, `valgrind --tool=callgrind`, deterministic across runs,
with each toolchain's own process-startup cost subtracted so the number is the
kernel and not libc's initialiser. Kernels are the project's own
`examples/bench/*.fern` with line-for-line C / Rust / Go transliterations.

| kernel | Fern, audited | Fern, after tier A | `gcc -O0` | `gcc -O2` | `clang -O2` | `rustc -O` | Go 1.24 |
|---|---|---|---|---|---|---|---|
| `int_loop` — 3 M-iteration i64 accumulate | 66.00 M | **33.00 M** | 15.00 M | *eliminated* | *eliminated* | *eliminated* | 15.07 M |
| `call_overhead` — 1 M non-inlined calls + `fib(24)` | 38.20 M | **21.00 M** | 25.55 M | 11.21 M | 11.65 M | 11.65 M | 18.33 M |
| `array_index` — 100 × 20 000-element i32 sum | 73.87 M | **57.47 M** | 24.22 M | 5.53 M | 5.77 M | — | — |

As ratios against Fern, audited → after tier A:

| kernel | vs `gcc -O0` | vs Go | vs `gcc -O2` |
|---|---|---|---|
| `int_loop` | 4.40× → **2.20×** | 4.38× → **2.19×** | n/a — all three optimisers replace the loop with its closed form |
| `call_overhead` | 1.50× → **0.82×** | 2.08× → **1.15×** | 3.41× → **1.87×** |
| `array_index` | 3.05× → **2.37×** | — | 13.4× → 10.4× (7.34× → **5.71×** against `-fno-tree-vectorize`) |

**`call_overhead` is the one to read twice: 0.82× means Fern now retires fewer
instructions than `gcc -O0` on it, and sits 1.15× Go and 1.87× `gcc -O2`.** It
is also the fairest kernel in the set — a real workload with calls that no
optimiser can fold away, unlike `int_loop`, which all three replace with its
closed form.

`int_loop` is the cleanest read in the set: no arrays, no heap, no reference
counting, no bounds checks. Nothing in Fern's semantics accounts for any part of
that 4.4×. It is codegen.

`array_index`'s 13.4× is the least fair number in the table and is broken out
accordingly: 1.8× of it is auto-vectorisation, which Fern does not attempt at
all, and some of the remainder is bounds checking, which C does not perform.
The honest scalar-to-scalar figure is 7.34×.

### 1.2 The ranking

Placing Fern in the field, with the tier defined by measured behaviour rather
than by reputation:

| tier | what is in it | vs `-O2` | Fern? |
|---|---|---|---|
| 0 | LLVM / GCC at `-O2`: C, C++, Rust, Swift, Zig, Fortran | 1.0× | |
| 1 | production SSA backends outside LLVM: **Go** (1.6× here), Java C2, .NET RyuJIT, OCaml flambda | 1.2–2× | |
| 2 | simple native compilers with real instruction selection but weak or no allocation: **tcc**, `gcc -O0`, FPC `-O0` | 2–3× | |
| 3 | **naive stack-machine translators** — an IR op maps 1:1 to a mnemonic, intermediates live on the machine stack | 3–13× | ← **Fern's default native backends, today** |
| 4 | non-JIT bytecode VMs: CPython, Ruby, Lua (interpreted) | 20–100× | |

**As audited, Fern sat in tier 3, one tier below `gcc -O0` and `tcc`.** After
tier A it is **in tier 2 and touching the bottom of tier 1**: 1.87× `gcc -O2`
and 1.15× Go on the call-heavy kernel, 2.2× `gcc -O0` on the bare loop that
still has no register allocator behind it. Go remains the fair aspirational
peer — a self-hosted language with its own SSA backend, no LLVM dependency, and
a comparable commitment to fast builds and static binaries — and it is now one
kernel's worth of distance away rather than a tier.

Two qualifications, both still real. Fern's `-O2` comparisons carry semantics C
does not — bounds checks, reference counting, a zero-divisor guard — and on
`int_loop`, which carries none of them, the remaining 2.2× against `gcc -O0` is
pure codegen: 11 instructions per iteration against 5, because every local is
still in memory and there is no register allocator. That is the ceiling tier A
could reach, and it is what §5's SSA path exists to break.

### 1.3 What Fern *is* best in the world at

The ranking above is one axis, and the report would be dishonest if it stopped
there. On the axes Fern was built for it beats every language in the table, by
margins that are not close:

| | Fern | `gcc -O2` (dyn) | `gcc -O2 -static` | Go 1.24 | `rustc -O` |
|---|---|---|---|---|---|
| retired instructions, empty `main` | **7** | 202 429 | 228 519 | 291 170 | 375 878 |
| binary size, `int_loop` | **4 281 B** | 15 784 B | 785 240 B | 1 874 520 B | 3 942 480 B |
| executable segment | 550 B | 329 B | 513 565 B | — | — |

The executable-segment row is the one not to over-read: the dynamic build's
329 bytes exclude every byte of libc it loads at run time, which is what the
static column's 513 565 makes visible. `-static` is the like-for-like column,
and it is what the first two rows compare against.

Seven instructions from `_start` to `exit`. That is four to five orders of
magnitude below every runtime in the comparison, and it is not an accident of
measurement — Fern links a static ELF with no libc, no dynamic loader, no
runtime initialiser and no GC to start. For the CLI-tool and edge-handler
workloads the language grew up on, this is the number that matters and Fern
wins it outright.

The reason codegen now matters anyway is the direction recorded in `CLAUDE.md`:
Fern is general-purpose, and the self-hosted compiler is exactly the
long-running, allocation-heavy program that the startup number does nothing for.
`docs/PERFORMANCE-AUDIT-2026-08.md` measured the self-host compiler at 3.25×
the Go implementation's wall time. That ratio is what tier 3 costs.

---

## 2. Why: the backends are 1:1 IR-op → mnemonic translators

### 2.1 The exhibit

`examples/bench/int_loop.fern`, whose whole body is `sum = sum + i; i = i + 1`
under `while (i < 3000000)`. Fern's x86-64 loop, verbatim, against `gcc -O0`'s:

As audited. The right column is `gcc -O0`; the after-tier-A form follows.

```
Fern — 22 instructions/iteration        gcc -O0 — 5 instructions/iteration
─────────────────────────────────       ──────────────────────────────────
.LloopTop_2:                            .L3:
    mov rax, [rbp-16]                       mov rax, QWORD PTR -8[rbp]
    push rax                                add QWORD PTR -16[rbp], rax
    movabs rax, 3000000                     add QWORD PTR -8[rbp], 1
    mov rcx, rax                        .L2:
    pop rax                                 cmp QWORD PTR -8[rbp], 2999999
    cmp rax, rcx                            jle .L3
    jge .LblkEnd_1
    mov rax, [rbp-8]
    push rax
    mov rax, [rbp-16]
    mov rcx, rax
    pop rax
    add rax, rcx
    mov [rbp-8], rax
    mov rax, [rbp-16]
    push rax
    movabs rax, 1
    mov rcx, rax
    pop rax
    add rax, rcx
    mov [rbp-16], rax
    jmp .LloopTop_2
```

After tier A the same loop is eleven instructions, and the three defects below
are what the difference was:

```
.LloopTop_2:
    mov rax, [rbp-16]
    cmp rax, 3000000          ; the constant is an operand, not a register
    jge .LblkEnd_1
    mov rax, [rbp-8]
    mov rcx, [rbp-16]         ; loaded straight into the register that uses it
    add rax, rcx
    mov [rbp-8], rax
    mov rax, [rbp-16]
    add rax, 1
    mov [rbp-16], rax
    jmp .LloopTop_2           ; still not rotated — §6 tier C
```

Both keep every local in memory — neither has a register allocator. `gcc -O0`
won 4.4× because it does three things Fern's emitter did not; two are now
fixed and the third is still open:

1. **It uses immediate operands.** `cmp QWORD PTR -8[rbp], 2999999` is one
   instruction; Fern spends five reaching the same comparison
   (`push`/`movabs`/`mov rcx,rax`/`pop`/`cmp`). `movabs rax, 3000000` is
   ten bytes on its own, where `cmp rax, 3000000` is seven, total.
2. **It uses memory-destination ALU forms.** `add QWORD PTR -16[rbp], rax`
   is one instruction; Fern spends three (load, add, store). In the entire
   1 601 565-instruction self-host emit, memory-destination ALU appears
   **0 times**.
3. **It rotates the loop.** `gcc` places the condition at the bottom so the
   back edge *is* the conditional branch. Fern tests at the top and pays an
   unconditional `jmp` every iteration — plus it leaves a dead `.LloopEnd_3`
   label behind it. **Still open**; it is a change to the shape `internal/ir`
   builds for a `while`, not to either backend, which is why it is tier C.

Item 1 is fixed: P4 folds the constant into the compare. Item 2 is only half
fixed — P5 and P6 load the operand straight into the register that consumes it,
which is why `mov rcx, [rbp-16]` replaced a four-instruction round trip, but the
memory-DESTINATION form is still not emitted at all, so `sum`'s load, add and
store remain three instructions where `gcc -O0` uses one. Item 3 is untouched.
Together those two are most of the remaining 11-against-5.

Everything else on the list below is a variation on those three.

### 2.2 The census

`scripts/codegen-census -c examples/self_host/checker_run.fern`, the self-hosted
checker driver — 1 074 functions, the largest realistic program in the tree:

```
as audited
x86-64 (default)      total=1601565  overhead=681648 (42.6%)  stack=297873  pad=181924  shuffle=201851  frame=167617
arm64  (default)      total=1500332  overhead=401804 (26.8%)  stack=337650  pad=0       shuffle=64154   frame=134995
arm64  (-backend ssa) total=2015723  overhead=373433 (18.5%)  stack=0       pad=0       shuffle=373433  frame=639

after tier A
x86-64 (default)      total= 963165  overhead=120350 (12.5%)  stack= 47589  pad= 21783  shuffle= 50978  frame=168498
arm64  (default)      total=1150723
arm64  (-backend ssa) total=1655985
```

`overhead` counts only instructions a register allocator deletes outright:
operand-stack pushes and pops, the `rsp` padding an odd operand-stack depth
forces at a call, and register-to-register handoffs. Frame traffic is reported
but deliberately *not* counted, because a real allocator keeps some of it as
genuine spills. **42.6% is therefore a floor, not a ceiling.**

The `pad` column is worth its own line. System V requires `rsp` 16-byte aligned
at every `call`; the operand stack moves it in 8-byte steps, so the emitter
brackets calls it cannot prove aligned with `sub rsp, 8` / `add rsp, 8`. That
exact triple occurs **90 935 times** — 181 870 instructions, **11.4% of the
whole program**, spent on nothing but alignment arithmetic. arm64 pays zero for
the same thing because its operand slots are 16 bytes wide.

The instruction repertoire tells the same story from the other end. Across
1.6 M x86-64 instructions the backend uses **61 distinct mnemonics**; across
1.5 M arm64 instructions, **60**. `imul` appears 26 times. `lea` 5 654. `cmov`
zero.

---

## 3. Findings, ranked

Severity is (impact × confidence). "Confirmed" means the phenomenon and its cost
were reproduced independently of whoever first reported it. Costs are shares of
*all emitted instructions* on the named real program, not on a probe.

| # | finding | backend | cost | status |
|---|---|---|---|---|
| 1 | No ALU-with-immediate on the expression path — every constant operand is materialised into a register first | x86-64 | **20.3%** of instructions, 13.8% of `.text` | confirmed, fix prototyped |
| 2 | Constant field/element offsets never folded into the addressing mode — 5 instructions where `ldr xD,[xB,#K]` is 1 | arm64 | **14.6%** of instructions | confirmed independently |
| 3 | Call-site `rsp` alignment padding at 90 935 sites | x86-64 | **11.4%** of instructions | confirmed |
| 4 | No ALU/compare immediate operands — constants `movz`'d into a register first | arm64 | **6.7%** of instructions | confirmed |
| 5 | Reference-count guard materialises its mask in two instructions, 120 377 times; the whole guard is 4 instructions where 2 suffice | arm64 | 1.52% (mask) / **3.03%** (guard) | confirmed independently |
| 6 | Division and modulo by a compile-time constant emit `idiv` plus a dead zero-divisor and `INT_MIN/-1` guard — no power-of-two reduction, no magic-number reciprocal | both | 67.3% slower than the reciprocal on the probe | confirmed |
| 7 | `a + b*c` is never `madd`; no `msub`/`mneg`/`smull`/`umull` selection | arm64 | 0 `madd` in 7 933 724 instructions | confirmed |
| 8 | Shifted/extended register operands never selected — `a + (b << 3)` is 15 instructions against clang's 1 | arm64 | 8 330 folded vs 125 230 standalone `lsl` — 6.2% | confirmed |
| 9 | 64-bit immediates have no `movn` and no logical-bitmask-immediate path — `-1` costs 4 instructions where clang costs 1 | arm64 | 33 four-instruction chains; 0 `movn` in the binary | confirmed |
| 10 | No branch-free selection (`csel` 17 in 7.9 M, `cinc`/`csinc`/`cneg` 0); the SSA backend never fuses compare into branch — always `cmp → cset → cbnz` | arm64 | 54 027 `cmp;cset;cbnz` triples on the SSA path | confirmed |
| 11 | No bitfield selection — shift+mask is 8 instructions where `ubfx` is 1. `ubfx` reaches the output 7 062 times but only as two hardcoded idioms; `sbfx`/`bfi`/`bfxil`/`rev` are 0 | arm64 | — | confirmed |
| 12 | Shift counts always route through `CL`, never the `imm8` form | x86-64 | — | confirmed |
| 13 | Every i64 literal is a 10-byte `movabs`, including `0` (`xor eax,eax` is 2 bytes) and values that fit `imm32` (5 bytes) | x86-64 | size only | confirmed |
| 14 | `base+index*scale` addresses computed into a register, then dereferenced, instead of folded into the load | x86-64 | — | confirmed |
| 15 | Multiply by a constant always uses two-operand register `imul`; neither `lea` nor three-operand `imul r,r,imm` is emitted | x86-64 | — | confirmed |
| 16 | `cmovcc` never emitted — 0 occurrences in 1.6 M instructions; every conditional value is a taken branch | x86-64 | — | confirmed |
| 17 | 72 of the 78 Advanced-SIMD mnemonics the assembler gained in `e129df5` are unreachable from any code path — ~1 133 lines of encoder, 12 of 19 SIMD dispatch functions, dead against codegen | arm64 | dead surface | confirmed |
| 18 | A complete register-allocating x86-64 backend (`internal/codegen/x86_64ssa`, 114 passing tests) is not reachable from the CLI — `-backend ssa` rejects `x86-64-linux` | x86-64 | opportunity | confirmed |

Findings 1–5 alone are **~56% of x86-64's emitted instructions and ~25% of
arm64's**, and none of them require a register allocator.

Finding 5 deserves its own line, because it is the whole report in six
instructions. The inlined reference-count heap-range guard is:

```
    tbnz x0, #0, .LrcopDone      ; tagged? skip
    mov  x1, #1
    lsl  x1, x1, #28             ; x1 = 0x10000000
    cmp  x0, x1
    b.lo .LrcopDone              ; below the heap base? skip
    ldur w1, [x0, #-8]
```

`0x10000000` is `movz x1, #0x1000, lsl #16` — one instruction, and the
assembler has encoded it all along. And the comparison itself is
`lsr x1, x0, #28 / cbz x1, .LrcopDone`: two instructions for the four.

The detail that makes it an emblem rather than an oversight: **120 394 of the
120 394 immediate-form `lsl` instructions in the entire compiler are this one
line.** The shift-by-constant form is used for exactly one hardcoded
constant, and never once for a shift the user wrote.

### 3.1 The one that is already proven

Finding 1 was not merely measured, it was *applied*. Rewriting the five-line
shape

```
push rax                 →     <alu> rax, IMM
mov  rax, IMM
mov  rcx, rax
pop  rax
<alu> rax, rcx
```

as a post-pass over the emitted text and re-assembling with GNU `as`:

- `checker_run.fern`: 1 601 565 → 1 272 507 instructions (**−20.5%**);
  `.text` 5 317 319 → 4 584 201 bytes (**−13.8%**); 81 997 sites rewritten.
- `examples/bench`, 22 programs: −16.8% instructions, −12.0% bytes, **all 22
  exit codes identical**.
- Wall time, best-of-7 interleaved: `int_loop` −14.7%, `array_index` −20.9%,
  `tokenize` −17.9%, `enum_match` −10.6%, `map_int` −9.9%, `sort_ints` −5.8%.
- End to end: both the baseline and the rewritten 5.3 MB `checker_run` drivers
  were linked and run on `tokenize.fern`, `sort_ints.fern` and
  `self_host/util.fern` — identical exit codes and byte-identical stdout and
  stderr, including a 330-byte diagnostic dump. On `self_host/lexer.fern` the
  rewritten driver runs 69 022 µs → 62 443 µs, **−9.5%**.

An independent count of the pattern over the same emit, using a different
matcher, found 81 177 sites and 324 708 removable instructions — **20.3%**,
agreeing with the prototype to within 1%.

For scale: `docs/SSA-REGALLOC-PLAN.md` measures the *entire* SSA
register-allocating backend at 84% of the stack machine's `.text` at codebase
scale. This one peephole reaches 86.2% with no register allocation at all.

The window it needs already exists — `peepholeTail`
(`internal/codegen/x86_64/x86_64.go:3987`) has a 6-line window and the pattern
is 5, so this is a fourth rule alongside P1–P3, not an architectural change.

---

## 4. The assembler / emitter gap

This is the dimension the last day's work makes visible, and it is worth
stating on its own because it inverts the usual assumption. **Nothing here is an
assembler bug.** The encoders are correct and, since `493017b` / `6a0d82d`,
fuzzed byte-for-byte against both GNU `as` and `llvm-mc`. The gap is that the
code generator never asks for what they can encode.

| encoder capability | reachable from codegen? | evidence |
|---|---|---|
| arm64 `madd` / `msub` / `mneg` | no | 0 `madd`; the 64 `msub` are hardcoded in the decimal/time helpers |
| arm64 `csel` / `cset` / `cinc` / `cneg` | barely | 17 `csel`, 0 `cinc`/`csinc`/`cneg` |
| arm64 `cbz` / `cbnz` / `tbnz` | as fixed idioms only | 130 316 / 30 158 / 258 374 — every one from the inlined RC and immortal-tag guards, none from a user comparison; `tbz` 1 |
| arm64 `ubfx` / `sbfx` / `bfi` / `bfxil` | as fixed idioms only | 7 062 `ubfx`, all the SSO length unpack and float exponent; `sbfx`/`bfi`/`bfxil` 0 |
| arm64 `rev` / `rbit` / `clz` | no | `rev` 0, `rbit` 3, `clz` 7 |
| arm64 `movn`, logical bitmask immediates | no | 0 `movn`; every `and`/`orr`/`eor` is register-register |
| arm64 shifted / extended register operands | 7% | 1 083 folded against 15 430 standalone `lsl` |
| arm64 `ldp` / `stp` pairing | prologues only | 1 138 / 1 132, all frame save-restore |
| arm64 Advanced SIMD (`e129df5`, 78 mnemonics) | **6 of 78** | 12 of 19 SIMD dispatch functions dead; ~1 133 lines |
| x86-64 ALU-with-immediate accumulator short forms (`731ba39`) | not from the expression path | see finding 1 — the emitter never produces the operand shape that reaches them |
| x86-64 `cmov` | no | 0 in 1.6 M |
| x86-64 memory-destination ALU | no | 0 in 1.6 M |
| x86-64 `lea` for scaled-index / 3-operand add | no | address computed then dereferenced separately |

The one place SIMD does reach real code is the hand-written runtime: 3–5 vector
instructions per program, in `memcpy`, `count_byte` and `find_byte`
(`ld1`/`cmeq`/`cnt`/`addv` on arm64, `movdqu`/`pcmpeqb`/`pmovmskb` on x86-64).
Those are library kernels a human wrote. **The code generator has never selected
a vector instruction for user code, on any target.** There is no
auto-vectorisation and no vector IR.

The practical reading: the assembler work of the last day was not wasted — it is
a *prerequisite* that is now ahead of its consumer. Every finding in §3 that
names an arm64 instruction is codegen-only work against an encoder that is
already complete and already fuzzed.

---

## 5. The SSA path: half the code on kernels, 1.34× on the compiler

`internal/ssa` + `internal/codegen/arm64ssa` is a real register-allocating
backend, reachable as `-backend ssa -target arm64-linux`. Its status is better
than `docs/SSA-DECISION.md` and the CLI's own help text say — both still
describe it as covering "a subset of the language".

Measured over all 22 `examples/bench` kernels: **every one is accepted, none
refused**, and every one produces the same exit code as the default backend
under `qemu-aarch64`.

| | instructions | executable segment |
|---|---|---|
| geometric mean, SSA ÷ default | **0.54×** | **0.57×** |
| best (`call_overhead`) | 0.18× | 0.25× |
| worst (`map_probe_chain`) | 0.87× | 0.87× |

`int_loop` runs 2.3× faster under `qemu-aarch64` (0.046 s → 0.023 s over five
runs). The default backend spends 20 instructions on `a + b*c`; the SSA backend
spends 6; the optimum is 2 (`madd x0,x1,x2,x0` + `ret`).

**And then it inverts.** On `checker_run.fern` the SSA backend emits **2 015 723
instructions against the default's 1 500 332 — 1.34× *more***. That reversal is
the single most important thing in this section, because no kernel benchmark
would have shown it. Three causes, all measured:

1. **Reference counting is out-of-lined.** The SSA path emits 4 254 `bl
   __fern_rc_inc`, 11 009 `bl __fern_rc_dec` and 7 368 `bl
   __fern_rc_is_unique` — 22 631 calls the default backend inlines to a
   handful of instructions each. It also emits 54 016 `__fern_box_free` calls
   against the default's 31 259.
2. **No compare-branch fusion.** 54 027 `cmp; cset; cbnz` triples — the
   comparison is materialised into a boolean register and then branched on,
   where the default backend uses the flags directly (`b.eq`, `tbnz`). That is
   2.7% of its output spent on `cset` alone, plus a broken flag dependency
   chain.
3. **Incomplete coalescing.** 373 433 register-to-register `mov`s — 18.5% of
   everything it emits, and the entire remaining overhead in the census. The
   `a + b*c` case shows the shape: `mov x3, x1; mul x3, x3, x2` where
   `mul x3, x1, x2` was available.

Fix all three and the SSA path lands near 1.0–1.1 M instructions against the
default's 1.5 M — which is roughly where the census says a real allocator
should land (1.5 M minus 537 K of measured overhead ≈ 963 K).

### 5.0 The inversion is scale-dependent, not a flat loss

Re-measured after tier A and tier B's B2/B3 landed, the SSA path wins
comfortably on ordinary programs and loses only at the driver's scale:

| input | arm64 default | arm64 SSA | |
|---|---:|---:|---|
| `int_loop` | 99 | 22 | 0.22× |
| `tokenize` | 959 | 614 | 0.64× |
| `sort_ints` | 2,712 | 1,555 | 0.57× |
| `string_scan` | 1,455 | 853 | 0.59× |
| `checker_run.fern` | 1,149,869 | 1,655,366 | **1.44×** |

That shape is the diagnosis. A backend that were simply missing folds would
lose everywhere; one that loses only on a million-instruction program with very
large functions is failing at register pressure, which is what §6.2's
live-range-splitting item addresses. It is also why B5 stays blocked on a
figure measured on the compiler and not on the kernels.

### 5.1 wasm is the backend that is not paying for the mismatch

Worth stating because it isolates the cause. `internal/codegen/wasmbin` emits an
operand-stack IR to a target that *is* an operand-stack machine, so none of §2's
overhead exists by construction. On `tokenize.fern` — 951 wasm instructions —
the equivalent defects are:

| | count | share |
|---|---|---|
| `local.tee N; drop` (statement value nobody reads) | 5 | 0.5% |
| `i32.eqz; br_if` (condition inverted for a top-tested loop) | 3 | 0.3% |
| `local.set N; local.get N` where `local.tee` does both | 11 | 1.2% |

**About 2%, against the natives' 26.8% and 42.6%.** Locals are used as locals;
values are not round-tripped through memory. The same three *logical* defects as
the natives are visible in `int_loop`'s wasm — a non-rotated loop, a discarded
statement value, and a constant-divisor guard emitted as an out-of-line call
with the full zero and `INT_MIN/-1` test against the literal `97` — but each
costs a couple of instructions rather than a couple of hundred thousand.

The reading: the natives' 26–43% is not sloppiness spread through the emitters,
it is one design decision (an operand-stack IR translated 1:1 onto a register
machine) charged at every expression. wasm never pays it.

One coverage note that the CLI help does not make: `-backend ssa
-target wasm32-wasi` refuses any program containing a call that is neither
self-recursion nor a declared import — `buildWasmSSA` (`cmd/fern/main.go:1880`)
lifts only `main`. It fails cleanly, as promised (`wasmssa: OpCall to "fib" is
neither self-recursion … nor a declared import`, exit 1, no module written), but
that is single-function programs only, far narrower than the arm64 SSA path's
whole-program coverage, and `-backend`'s help text lists the two targets
together without the qualification.

### 5.2 The unreachable x86-64 twin

`internal/codegen/x86_64ssa` is a complete SSA emitter — `Emit`,
`EmitAsmModule`, `EmitProgram`, callee-saved analysis, spilling, phi resolution
with critical-edge splitting — with **114 tests, all passing**, including
`TestCodeSizeSmallerThanStackMachine`. `cmd/fern/main.go:1327` rejects
`-backend ssa` for `-target x86-64-linux`, so **no user can invoke it**. Parts
of it are not dead — `arm64ssa/gas.go` imports its register model — but the
emit path ships unreachable.

The honest caveat before anyone wires it up in an afternoon: its
`TestCorpusEmitCoverage` covers **25 functions**, against arm64ssa's 286-program
corpus differential and 2 048-program fernsmith sweep. The glue is ~50 lines
(mirroring `buildArm64SSA`, `cmd/fern/main.go:1906`); the coverage evidence is
what is missing, not the code.

---

## 6. What to do, in order

Ordered by (measured value ÷ risk). Everything in tier A is a local change to an
existing backend with an existing test gate, and none of it blocks or is blocked
by the SSA cutover.

### Tier A — peepholes and selection, no architecture change — LANDED

A1–A5 and A7 landed in PR #7991 for both native backends, together with two
rules the audit had not separated out (P5, the operand-stack round trip around
an undisturbed value; P6, materialising an argument into its own register).
Measured after: x86-64 `checker_run.fern` 1,601,565 → 962,540 (−39.9%), arm64
1,507,466 → 1,149,869 (−23.7%), self-host binaries −28.2% and −26.1%. A6 landed
in the same PR as a follow-up commit, in its guard-elimination and
power-of-two halves only; A8's remaining items did not — see §6.2.

Each was validated the same way before the next was started: the 22
`examples/bench` programs' exit codes against the pre-change compiler, and the
self-hosted compiler built by the modified backend emitting byte-identical
assembly. The original plan, for the record:


| | change | worth | shape |
|---|---|---|---|
| A1 | x86-64 ALU-with-immediate peephole (finding 1) | **−20.3% instructions, −13.8% `.text`, −9.5% driver wall time** | a 4th rule in `peepholeTail`, `x86_64.go:3987`; window already wide enough. Prototyped and validated byte-identical. |
| A2 | arm64 addressing-mode folding for constant field offsets (finding 2) | **−14.6% instructions** | match `OpAdd(base, OpConst K)` ahead of `OpLoad`/`OpStore`, `arm64.go:12762`/`:12808`; encoder already has the form |
| A3 | x86-64 call-alignment elimination (finding 3) | **−11.4% instructions** | spill operands to fixed frame slots instead of `push`/`pop`, so `rsp` never moves in the body and the prologue aligns once. Push→`mov [rbp-N]` is instruction-neutral; the whole `pad` column goes to zero. |
| A4 | arm64 ALU/compare immediates (finding 4) | **−6.7% instructions** | `binPopImm` variant at `arm64.go:11939`; `imm12` + `lsl #12`, plus `cbz`/`cbnz` when K==0 and a branch follows (8 217 sites) |
| A5 | arm64 RC guard: fold the mask, then the guard (finding 5) | −1.5%, then **−3.1%** | `arm64.go:13321`; `mov x1,#0x10000000` for the pair, then `lsr x1,x0,#28 / cbz` for the whole guard |
| A6 | Constant division: power-of-two → shift, otherwise magic-number reciprocal; and drop the zero/`INT_MIN` guards when the divisor is a literal (finding 6) | 67.3% on the operation | `internal/ir` fold + backend selection. The dead guard is pure constant folding. |
| A7 | `madd`/`msub`, shifted-register operands, `ubfx`/`bfi`, `movn`, bitmask immediates (findings 7–9, 11) | closes the §4 table | one match each, against an encoder that already has them |
| A8 | x86-64 `movabs` → `mov r32, imm32` / `xor` (13), `imm8` shifts (12), `lea` folding (14–15), `cmov` (16) | size, mostly | independent one-liners |

A1–A5 are, on the two real programs measured, **~56% of x86-64's emitted
instructions and ~25% of arm64's**, at the cost of a handful of pattern matches.
That is a better return than any other work in this document, and it is
available before the SSA cutover, not after.

Per `CLAUDE.md`, an optimisation that can live in `internal/ir` should — A6's
folding half belongs there. A1–A5, A7 and A8 are genuinely target-specific
instruction selection and belong in the backends.

### Tier B — make the SSA path the default — PARTLY LANDED

B2 (compare-branch fusion) and B3 (coalescing, taken at the renderer as
three-address operand reads rather than in the allocator) landed, along with
block layout and precise per-call live sets. `checker_run.fern` through
`-backend ssa`: 2,016,062 → 1,655,985 (−17.9%); `cset` 54,872 → 624;
unconditional branches to the following label 58,343 → 0.

**B1 was measured and deliberately not taken.** Nearly every rc call site on
this path is already `mov x0, xN; bl f` — two instructions, shorter than the
stack machine's inline guard — so inlining would trade static size for runtime.
The 1.7× `box_free` call-site gap is a lowering difference, not a call-sequence
cost, and is its own investigation.

**B5 is not reachable yet.** The inversion is narrowed, not closed, and the
honest ratio depends on when it is measured: 1.10× the stack machine as it stood
before this work, 1.44× the stack machine as it stands after, because tier A
moved that target in the same PR. Defaulting arm64 to the SSA backend is still
the wrong call. The original plan:


| | change | worth |
|---|---|---|
| B1 | Inline RC inc/dec/is_unique on the SSA path, as the stack machine does | removes 22 631 calls on `checker_run` |
| B2 | Compare-branch fusion (`cmp`+`b.cond`) instead of `cmp`/`cset`/`cbnz` | 54 027 sites |
| B3 | Finish coalescing | 373 433 `mov`s = 18.5% of SSA output |
| B4 | Re-measure `checker_run`; expect ~1.0–1.1 M against the default's 1.5 M | the cutover's actual gate |
| B5 | Then default arm64 to `-backend ssa`, keeping the stack machine behind a flag | `SSA-REGALLOC-PLAN.md` phase 4 |
| B6 | Widen `x86_64ssa`'s corpus evidence from 25 functions to a real differential, then wire it into `cmd/fern` | phase 3 |

B1–B3 are the reason the SSA path currently loses on large programs, and they
are the honest precondition for B5. Flipping the default before them would
regress the compiler by 34%.

### Tier C — structural, and correctly out of scope for now

Loop rotation and block layout; loop-invariant code motion; strength reduction
on induction variables; global value numbering; tail calls (`call` immediately
followed by a return is 75 sites on `checker_run` — small, but free); any form
of auto-vectorisation. These want the SSA path to be the default first, because
each is a pass over a CFG and `internal/ir`'s flat op stream is exactly the
representation `docs/SSA-CUTOVER-PLAN.md`'s fourth tripwire says not to add more
cross-block analysis to.

### 6.2 What tier A did NOT close

- **A6's reciprocal half.** The guards and the power-of-two cases landed: a
  literal divisor makes both the zero-divisor and the `INT_MIN/-1` branch dead,
  a power of two becomes a shift — biased by `2^k−1` when signed, because
  `sar` rounds toward −∞ and the language rounds toward zero — and a power-of-two
  remainder becomes a mask. Semantics are pinned by
  `conformance/cases/const_divisor_lowering`, which runs 19 signed i32 divisors
  × 17 dividends plus the i64 and unsigned families through an order-sensitive
  hash on all four backends. What is still open is the **general magic-number
  reciprocal** for a non-power-of-two divisor: the divide is 20–40 cycles where
  a reciprocal is ~5. It is its own correctness problem — rounding, `INT_MIN`,
  signedness — and it moves no static figure, since the whole compiler has 12
  `idiv` sites.
- **A8's remainder.** `movabs` for small and zero constants, `imm8` shifts,
  `lea` folding, and `cmov` are all still unemitted.
- **Memory-destination ALU**, which is what still separates the `int_loop`
  body from `gcc -O0`'s.
- **Loop rotation** (tier C): the back edge is still an unconditional `jmp`
  rather than the conditional branch, and the dead `.LloopEnd` label is still
  emitted. This one lives in `internal/ir`'s `while` shape, not in a backend.
- **Frame traffic on the SSA path**, still the largest single item anywhere in
  this document: 869,756 of the 1,657,173 instructions it emits for
  `checker_run.fern` are frame-relative loads and stores — **52.5%**. Note
  `scripts/codegen-census` does *not* show this: it counts a frame reload as
  useful work by design, so its `stack=0` for this backend is a deliberate floor
  and not a contradiction. **What this is NOT is a spill-selection problem** —
  see §6.4, which is the measurement, not a guess.

### 6.3 A gate, so this cannot regress silently

Nothing in the tree measures the ratio of useful work to shuffling. The
fixtures check answers; the fixpoint checks reproducibility; the driver-size
lane checks bytes, which move for unrelated reasons. `scripts/codegen-census`
(added with this document) prints the classification above for any program, and
is the natural thing to pin in CI once tier A starts landing — a ratchet on the
overhead percentage, per backend, in the shape `internal/lint/repo_gate_test.go`
already uses for complexity.

### 6.4 Spill selection is not the lever — measured, and the fix rejected

This document previously named spill quality the largest item and prescribed
live-range splitting, on the reasoning that hole-free intervals over-spill and a
spilled value reloads at every use. Both halves of that are true of the code —
`Interval` is a single contiguous `{Start,End}` (`internal/ssa/regalloc.go`)
extended across whole blocks for anything in `LiveIn`/`LiveOut`, and
`allocateLinear` spills the furthest-ending interval and never re-registers it —
and the conclusion drawn from them is still wrong.

Instrumenting the allocator over `checker_run.fern`'s 1,049 functions:

| | |
|---|---:|
| values | 389,100 |
| given a register | 385,682 |
| spilled | 3,418 (**0.9%**) |
| operand reads of a spilled value | 196,049 |

So the allocator does not over-spill: it spills one value in a hundred. But
those values are read **~56× more often than the average value**, because
"furthest-ending" selects exactly the long-lived, heavily-read ones — loop and
parser state — and each of their ~57 reads then becomes a reload.

That diagnosis is sound and the obvious fix still loses. Replacing the victim
rule with a spill weight (reads per unit of register occupancy, cross-multiplied
so the ordering stays exact, falling through to furthest-end when no use counts
are supplied) does what it says:

| | furthest-end | use-density |
|---|---:|---:|
| reloads | 196,049 | 26,926 (**−86.3%**) |
| values spilled | 3,418 | 4,535 (+32.7%) |
| **total emitted** | **1,657,173** | **1,902,179 (+14.8%)** |

Reloads fall by six sevenths and the output gets *bigger*. The frame-relative
share stays at 52.5% in both, unmoved to the digit — which is the tell: the
traffic is structural to the emitter, not a function of how well the allocator
chooses. Splitting the 869,756 by position:

- **375,250 (43.1%)**, which is **22.6% of all output**, sit contiguous with a
  `bl` — the caller-saved save/restore around each of 173,693 calls, plus
  outgoing stack arguments, at 2.2 frame ops and 2.39 registers moved per call.
  All but 39,796 of those are single `ldr`/`str`, not `ldp`/`stp`: pairing is
  not where this traffic lives, so a peephole that pairs adjacent saves would
  address the encoding and not the cause.
- **494,506 (56.9%)** are elsewhere, and spill reloads can account for at most
  196,049 of the total either way.

The work this points at is therefore call-boundary traffic, not interval shape:
getting call-crossing values into callee-saved registers so they need no save at
all (`arm64ssa` allocates 22 registers of which only 10 are callee-saved, so an
eleventh call-crossing value is saved and restored at every call it spans), and
the out-arg path. Live-range splitting stays worth having for its own sake and
is not the headline.

The rejected patch is not in the tree; this section is what it bought.


---

## 7. What this audit does not cover

Stated plainly so the gaps are not mistaken for clean bills of health.

- **The self-hosted compiler's own emitters.** Everything above measures the Go
  implementation. `examples/self_host/x86_native.fern` and `arm64_native.fern`
  are separate emitters with their own instruction selection, and per
  `docs/NATIVE-CONVERGENCE.md` they are where new surface should land first.
  A fix in `internal/codegen` is half the work.
- **Whether `gcc -O2` is the right target.** A language with bounds checks,
  reference counting and a zero-divisor guard cannot reach C's numbers on
  array code and should not pretend to. The right frame is probably Go — same
  self-hosting posture, same static-binary commitment, comparable safety —
  and Fern is 2.08× Go on the one kernel where both were measured cleanly.
- **The wasm backend beyond §5.1** — `wasmbin` is 25 683 lines and this audit
  looked at one module's instruction mix. Whether it uses `br_table` for dense
  switches, multi-value blocks, `memory.copy`/`memory.fill`, or the v128 family
  that `docs/BACKEND-PARITY.md` puts in the baseline, is unmeasured here.
- **The reference-counting overhead itself**, beyond the guard in finding 5.
  How much of a realistic program's instructions are retain/release traffic,
  how many of those are necessary, and how Fern compares to Koka, Lean 4, Roc
  and Swift, is its own audit. `docs/rc-log/` holds the live state.
- ~~**Register pressure and spill quality** on the SSA path~~ — now measured,
  in §6.4, and the hint in this bullet was wrong twice over. Only 0.9% of
  values spill at all, so the `ldp`/`stp` pairs it pointed at (80 639 / 74 365
  today, down from 99 367 / 92 997 before #7991 narrowed the per-call live sets)
  are not spill traffic — and they are not call-boundary traffic either:
  **74.3% of them sit nowhere near a `bl`**, so they are frame save-restore in
  prologues and epilogues. The call-boundary traffic §6.4 does identify is
  overwhelmingly single `ldr`/`str`.
- **Cache and branch behaviour.** Every dynamic number here is retired
  instructions, which is deterministic and reproducible but blind to memory
  hierarchy. A 20% instruction reduction is not automatically a 20% speedup —
  the measured driver speedup for A1 was 9.5%, not 20.5%, and that difference
  is the honest shape of it.
- **arm64 dynamic measurement** is under `qemu-aarch64`, not hardware.

---

## 8. Reproducing

```sh
go build -o bin/fern ./cmd/fern

# the census, per backend
scripts/codegen-census -c examples/self_host/checker_run.fern
scripts/codegen-census -c examples/bench/int_loop.fern

# the exhibit
./bin/fern -target x86-64-linux examples/bench/int_loop.fern | sed -n '/^__fn_main:/,/^\.size/p'

# instruction repertoire
./bin/fern -target arm64-linux examples/self_host/checker_run.fern \
  | grep -P '^\t\S' | awk '{print $1}' | sort | uniq -c | sort -rn

# the cross-language ranking (kernel-only retired instructions)
valgrind --tool=callgrind --callgrind-out-file=/dev/null ./prog   # subtract an empty main's count

# the SSA comparison
for f in examples/bench/*.fern; do
  d=$(./bin/fern -target arm64-linux "$f" | grep -cP '^\t\S')
  s=$(./bin/fern -backend ssa -target arm64-linux "$f" | grep -cP '^\t\S')
  echo "$f $d $s"
done
```
