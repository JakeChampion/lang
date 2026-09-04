# A heap pointer read 32 bits wide: the arm64-darwin crash, reproduced on Linux

`map_keys_values_header_churn_free` (`internal/e2e/rc_correctness_test.go`)
SIGSEGVs on real Apple Silicon — the single failure the macos-15 rc-corpus lane
found — while passing on x86-64 and on arm64 under qemu. The distinguishing
feature is the address regime, not the OS.

## The regime, and why every cheap lane misses it

`__fern_alloc` hints its lazy arena at `0x1000_0000` (256 MiB), the same value
on Linux and Darwin. Linux honours the hint; macOS ignores it and relocates the
mapping above 4 GiB. So on every lane the project can run cheaply, a heap
pointer fits in 32 bits, and code that handles one 32 bits wide is correct.

qemu-aarch64 honours the hint too, so raising it reproduces the regime without
a Mac. That is now `arm64codegen.Options.HighHeapProbe` (8 GiB;
`FERN_HIGH_HEAP=1` from the driver), gated by `TestArm64HighHeap*` in
`internal/e2e/arm64_high_heap_test.go`.

## Bisection

Same source, same compiler, two arena hints:

| program | 256 MiB heap | 8 GiB heap |
|---|---|---|
| the full `map_keys_values_header_churn_free` case | exit 0 | **SIGSEGV** |
| `Map[i32,i32]` insert + `keys()` + `values()` | exit 0 | exit 0 |
| `Map[i64,i64]` insert + `keys()` | exit 0 | **SIGSEGV** |
| `Map[i64,i64]` insert + `values()` | exit 0 | **SIGSEGV** |

So it is the WIDE (i64 / u64 / f64) column, on both the key and the value side
— the two the IR intercepts and lowers inline.

## The truncating instruction

`emitWideMapKeys` / `emitWideMapValues` dereference the Map handle themselves
rather than calling into `core/map`, and they did it with a bare `OpLoad`.
`Op.Width`'s zero value is i32, so on arm64 that is `ldr w0` — four bytes of an
eight-byte address. From `objdump` of the failing binary:

```
400294:  ldur  x0, [x29, #-8]   ; x0 = m, the Map handle
400298:  ldr   w0, [x0]         ; buf = *m   <-- 4 bytes of a heap pointer
40029c:  stur  x0, [x29, #-64]  ; bufSlot
```

Everything downstream — `entriesBase = buf + 24 + cap*4`, then the per-entry
`__memcpy` — walks from the truncated base. Below 4 GiB the high half is zero
and the truncation is invisible; above it, the read is wild.

`internal/ir/ir.go:19272` (values) and `:19446` (keys), pre-fix line numbers.
The neighbouring loads in the same builders (`cap`, `len`) are genuinely i32
and are unchanged. `emitMapLenLoad`, twenty lines away, already spelled its own
handle deref `Width: WidthPtr` — the two inline builders were the sites that
did not.

## The class, not the row

The defect is not a stride mismatch. It is that **an IR op touching a heap
pointer defaults to i32 width**, so `WidthPtr` there is the correctness case
rather than an optimisation. Auditing every bare `OpLoad` / `OpEq` / `OpNe` in
`internal/ir/ir.go` for a pointer-shaped operand found the two derefs above and
six pointer-IDENTITY comparisons, all emitting a 32-bit `cmp`:

- `emitMapCowRetainTest` — result handle vs pre-CoW receiver, the test that
  decides whether `m = m.insert(..)` retains.
- `emitConsumedArrayOverwriteDec` and the three `isSelfMapMutation` /
  self-array / map-overwrite siblings — old buffer/handle vs new, the test that
  decides whether the old value is dec'd.
- `emitArgTempDropsGuarded` — call result vs stashed arg temp, guarded only
  when the result is pointer-typed, so both sides are pointers by construction.
- the `dyn Trait` concrete-vtable comparison.

Every one is rc bookkeeping keyed on pointer identity, and a 32-bit compare
reads two distinct pointers as the same object whenever they agree in their low
half. That needs live allocations ≥ 4 GiB apart, which a 16 GiB arena and a
long-running program (the self-hosted compiler is exactly one) make reachable.
The consequence is a spurious retain or a skipped dec — a leak in the cases
looked at, but the wrong verdict either way.

All eight sites now carry `Width: WidthPtr`, which resolves to 8 bytes on the
natives and to i32 on wasm32, where a pointer IS four bytes.

## What the fix does not change

Emitted wasm is byte-identical across 286 of 287 `examples/**` programs (the
one outlier is non-deterministic on `origin/main` too, verified by compiling it
twice with the unmodified compiler). On the natives only operand widths move:
for `examples/proposals/trie.fern` on x86-64 the instruction histogram is
identical and exactly one `cmp %ecx,%eax` became `cmp %rcx,%rax`; for the
`map_keys_values_header_churn_free` program on arm64 the histogram is identical
and five `cmp w1, w0` became `cmp x1, x0` alongside the two `ldr w0, [x0]` →
`ldr x0, [x0]`.

## Adjacent, NOT fixed: `usize[]` strides four bytes on the natives

Found while checking whether `__map_column`'s 4-byte scalar column truncates a
pointer-width K/V. It does — but so does the array it feeds.
`ast.ElemSizeBytesFor` switches on `NumberType.NormalWidth()`, which returns
`ast.WidthPtr` (-1) for `usize` and falls to the `return 4` default, so a
`usize[]` element is four bytes on arm64 and x86-64. A `Map[usize, usize]`
whose keys exceed 2^32 loses the high half of every one, on the DEFAULT heap:

```fern
var big: usize = 4294967296 as usize;
var m: Map[usize, usize] = map_new(8);
m = m.insert(big + 5, big + 50);
var ks = m.keys();          // ks[0] != big + 5
```

`get` / `get_or` / `set` round-trip such values correctly; only the array does
not. Widening the map column alone makes it worse, not better — the producer
would then disagree with the consumer — so the fix belongs in the array element
layout (`ElemSizeBytesFor`, `payloadSlotSize`, `arrayElemStoreOp` and each
backend's index lowering), which is a wider change than this one and is left
for its own PR rather than shipped as a narrow map-side patch.
