# Appends that fit take no frame

A dynamic census of a stage-2 x86-64 compiler compiling `lexer.fern`
(callgrind `--dump-instr`, joined with the disassembly and `nm` of a `-g`
build) puts 43% of the 2.83 G instructions in the runtime helpers, and
`__fern_arr_push` alone at 13.8%, 27% of that in push and pop. Every append
built a frame and saved four callee-saved registers before finding out it
had room; the grow path is the only one that needs them.

Both `__fern_arr_push` and `__fern_arr_push_owned` now start with the same
frameless head (`arr_push_fast`): length against capacity first, since an
append that must grow is the common miss (an empty literal has capacity
zero), then the count (sole owner, or the bit-31 immortal sentinel), then
the store, the length bump and `ret`, touching `%rax`/`%rcx`/`%rdx` (x86-64)
or `x2`/`x3` (arm64). A miss enters the old framed body, which jumps
straight to the grow path; `__fern_arr_push_owned` calls that body directly
rather than repeating the head. Under `FERN_RC_FREE_DEBUG` the head is not
emitted, so the sanitizer's count check still runs on every append.

x86-64 callgrind Ir against the same tree without it:

| bench | Ir change |
|---|---|
| `array_append` | −44.5% |
| `sort_ints` | −19.3% |
| `sort_strings` | −6.2% |
| `sort_inplace` | −5.0% |
| `utf8_ingest_validated` | −3.0% |
| 10 others | 0% to −0.2% |

Stage 2 compiling `ssa.fern` to assembly: 3,543.7 M → 3,526.6 M (−0.5%),
against a baseline without #10081's zeroing, so the gain is at least that.

Two traps from measuring it:

- A baseline compiler built before a rebase carries the old main, and main
  had meanwhile made `__fern_alloc_u8` zero its buffer (#10081). Against
  that stale baseline this change looked +2.9% on `struct_drop`; against the
  same tree without it, −0.15%. Build the baseline from the commit the
  change sits on.
- `__fern_arr_push` is not a symbol in the ELF, so a per-function profile
  charges it to `__fern_alloc_u8`, the label before it.

## Not taken: releases that keep the caller-saved registers

`__fern_arr_dec` and `__fern_str_free` are 45% of the checker's call sites
(17,114 of 38,309). Rewriting both to touch only `%rax`, `%rcx`, `%rdx` and
`%r11`, and letting the allocator treat a call to them as clobbering `%rax`
alone (the `abi_cl` rule division uses), cut `checker.fern` by 0.7% static,
and the benches split: `record_update` −2.3%, `tokenize` −0.9%, `enum_match`
+0.7%, `pvec_with` +0.6%. It is the same result `ssa.rc_inlined`'s note
records for not counting the inline rc primitives as calls.
