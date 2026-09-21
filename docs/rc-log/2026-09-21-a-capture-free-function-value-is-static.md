# A capture-free function value is static data

2026-09-21 — `ir`, `asmcore`, `asm_ir`, `asm_arm64_ir`, `wasm_ir`, `irlower`,
`ssarc`, `irexec`, `irlower_run`.

## What was wrong

Every function value in the self-host is an environment box — `[body, caps…]` —
so that a call through one dispatches the same way whatever position it came
from. When the value captures NOTHING the box is one word wide and that word is
a code address, so every bit of it is known at compile time. The lowering built
it anyway: `const_func <body> ; arr_make(1, 32)`, one heap block per
evaluation. Native folds the same value to a static cell before emit
(`ir.InlineZeroCaptureClosures`) and pays nothing.

The visible half was #9839. A `fip` body handing a bare lambda to a combinator
was charged for an "array literal" the author never wrote:

```
error[E068]: twice: `fip` function "twice" allocates: 2 un-reused allocation
site(s) exceed the allowance of 0: array literal at op 1; `map` materialized by
the combinator: … at op 5
```

so a graded `fip(1)` native accepted the self-host refused, naming a construct
that is not in the source. The invisible half was the block itself, on every
capture-free value in every program — the `$wrapN` trampoline the lift builds
for a bare fn-name is the same shape, so `core/map` paid one per function-valued
argument.

## The fix

`ir.op_const_closure(body)` — `const_struct`'s function-value case. #6149
already places a compile-time-constant box in static data with an immortal rc
word, and all three backends already emit those blocks; a capture-free function
value is such a box, so it rides that machinery rather than a new op. The type
half carries the sentinel `&fn:<body>` where a record names its struct, and
`ir.const_struct_fn_body` is the one reader of it.

The three lowering sites that built the box emit it when there are no captures:
`irlower`'s `__mkclo$` marker arm and `lower_expr_lambda`, and `ssarc`'s
`closure_new` on the semantic path.

Block layout is per backend, because a record box and an array box do not agree
about word 0:

| | record constant | capture-free function value |
| --- | --- | --- |
| register | `[cap][rc] shape, f_i…` | `[cap=2][rc] len=1, __fn_<body>` |
| wasm | `[rc][bsz] tid@0, f_i@8+i*8` | `[rc][bsz=24] len@0, cap@4, funcref@8` |

On wasm the word holds the body's ABSOLUTE funcref index: a `const_func` op
emits one relative to `$__fn_base<ns>`, but a data segment holds bytes and has
no base to add, and the merged table is in hand where the segments are written.
`fn_value_table` collects const_closure bodies too, so the table owes each one a
slot.

Both register backends had their own copy of the pack-the-block-text loop, in
two arms each; those four are now one `asmcore.const_agg_pack`.

## Measured

`__heap_alloc_count()` across a loop that builds one, all three targets:

| | before | after |
| --- | ---: | ---: |
| 2000 × a returned capture-free lambda | 2000 | 0 |
| a capturing lambda (the guard) | 500 | 500 |

and the cross-compiler allocation matrix gains a row at parity:

```
capture_free_fn_value 1  1   the struct box alone
```

It read `1 2` before.

## What moved that was not a bug

`irlower_run -clocensus` classifies an env-first dispatch by where its box comes
from, because that decides how much analysis a devirtualising rewrite would
need. A constant box names one target with LESS to read than the flat
`const_func … arr_make` shape `env_local` was defined around, so `clo_flat_box`
recognises it and the site stays in that bucket. Left alone it would have landed
in `env_other` — "what no rewrite short of a points-to analysis can name" —
which is the opposite of true, and #6638's decision rests on that split.

## Still open

The `map` site in #9839's program is real and stays: this compiler has no
in-place map, and E068 says so. That is R7 of `docs/REUSE-CONTRACT.md`, native-
only until the reuse port reaches it.
