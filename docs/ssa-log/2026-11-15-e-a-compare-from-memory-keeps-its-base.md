# A compare from memory keeps its base

Date: 2026-11-15

## The fault

`ssa_load_compare` emits a compare that reads one operand from memory
(entry 2026-11-15-a). It read the other operand to `%rcx` when that operand
was in a frame slot.

The fixed-register table keeps only `%r11` free at a load, so the base of the
load can be in `%rcx`. The read of the other operand then wrote on the base:

```
movq 0(%r11), %rcx
movq -56(%rbp), %rcx
cmpq %rcx, 8(%rcx)
```

The HTTP/2 server stopped with a segmentation fault in `h2.Conn.respond` on
its first request. `TestServeH2X86_64` and `TestX86_64KtlsServeH2` failed.

## The repair

When the base is in `%rcx`, the other operand goes to `%rdx`. The table
keeps `%rcx`, `%rdx` and `%r11` free at a compare, and the base is the only
value that ends at the load. Thus no value is in `%rdx`.

## The test

`TestSelfHostCompareFromMemoryKeepsItsBase` compiles a function with six
values live across its calls and six more live at the compare. It requires
`cmpq %rdx, 8(%rcx)`, and it runs the program.
