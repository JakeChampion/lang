# An `own` struct parameter's update reuses its box (#6644 distcheck, slice 5)

`2026-09-02-consumed-array-param-ownership-flag.md` recorded that a self-built
stage1 assembling natively exhausts the arena in 13 s on `lexer.fern`, before
and after that slice, with 117,270 copied `code` buffers under
`__fern_arr_push`. This is that.

## The shape

Every assembler in the compiler threads its state as an `own` struct:

```
function emit_word(own a: X86Asm, v: i32): X86Asm {
    a = X86Asm { ...a, code: x86_le32(a.code, v) };
    return X86Asm { ...a, n: a.n + 1 };
}
```

`own` is a checker contract (E051: the caller passes an owned value at its last
use) with, until now, one consequence in the lowering — the return-transfer
retain is skipped for a bare `return a`. The functional update itself took the
plain path: a fresh box, the carried fields copied, the old box never released,
the overridden field's old value never released. Reduced, 3,000 rounds of two
updates:

| variant | allocs | frees |
|---|---|---|
| native | 6,015 | 6,015 |
| self-host, before | 15,004 | 3,000 |
| `a = Asm { ...a, code: le32(a.code, v) }` alone, before | 7,503 | 0 |

The pattern's cost on the assembler is worse than a box per instruction. With
the identity convention (`2026-09-02-identity-return-counted.md`) the helper's
in-place append hands `a.code` back with a retain; the update stored it and
released nothing, so the buffer sat at rc 2, the next helper's push copied the
whole ~445 KB code buffer, and the update leaked the superseded one. One copy
and one leaked buffer per emitted instruction.

## The rule

`emit_self_overwrite_reuse` already does the right thing for a local's
self-update `p = T { ...p, … }` (#6134): evaluate the overrides into temps,
test `__fern_rc_is_unique(p)`, and on the unique arm overwrite p's box in place
after releasing each overridden field's old value; otherwise copy into a fresh
box with retains and release p's reference. An `own` parameter is exactly a
box this frame may overwrite — the caller moved its reference in — so
`own_update_params_of` admits it into the same `SAREUSE:` set, and a new arm in
`lower_stmt_return` routes `return T { ...p, … }` through the emitter into a
hidden local the sweep keeps.

Two things the admission has to establish, because no runtime guard covers
them:

- **every override of a pointer field hands the box an OWNED value**: a
  literal, a clone-form `.append` / `.with` on the base's own field, a call
  result (owned by the identity convention), a base-less struct literal, a
  string literal or concatenation. A bare local or another struct's field is an
  uncounted alias the reused box would share with that local's sweep, and
  refuses the function (`own_override_owned`).
- **p is only read, passed on, or spread**: a `var q = p` alias or a rebind from
  anything but a self-update refuses it. Passing p to a callee is fine either
  way — an `own` position moves it, a borrowing one leaves it, and a counted
  store raises the count the uniqueness guard reads.

The releases are buffer-only for an `own` base (`shallow`): a clone-form
`names: a.names.append(x)` shares the old buffer's elements uncounted, so the
deep `__fern_str_arr_free` the local-donor path uses would free what the new
buffer holds.

## Measured

| variant | allocs | frees |
|---|---|---|
| self-host, after | 6,004 | 6,002 |
| `a = Asm { ...a, code: le32(a.code, v) }` alone, after | 15 | 13 |
| clone-form return update alone | 6,002 | 6,000 |
| the refused shape (`code: code` from a local) | 12,002 | 0 |

On the compiler: 59 of the 72 `own` struct parameters in the self-host are
admitted, 46 of the 54 in `x86_native`. The eight assembler refusals are the
"take the buffer out" pattern (`var code = a.code; a = X86Asm { ...a, code: [] };
… return X86Asm { ...a, code: code }`), a bare-local override.

The sanitized self-built stage1 (`FERN_SANITIZE=1`) assembling `lexer.fern`
natively (`-o <binary>`, the path `make distcheck`'s stage2 takes):

| | before | after |
|---|---|---|
| outcome | arena exhausted (exit 125) at 13 s | completes, 16.7 s |
| allocations / frees | — | 1,684,691 / 459,924 |
| live at exit | — | 96 MB |

`checker.fern` on the same path still exhausted the 16 GiB arena, at 38 s, and
the exit stack named the next shape exactly: `x86_gas_emit_packed`'s
`a = X86Asm { ...a, code: a.code.append(b) }`. The box was reused and the old
buffer released, but the override VALUE was still the clone form —
`lower_arr_append_value` slices the whole ~1 MB code buffer and pushes onto the
copy, once per emitted byte — so the assembler moved and freed a megabyte per
byte, and a freed large block is never the size the next one needs.

## The in-place form

So the emitter evaluates that one override PER ARM instead of ahead of the
uniqueness test (`own_field_inplace_append`): on the unique arm an override
`f: d.f.append(x)` over a pointer-width-element array field pushes onto the
field the box already owns (`arr_push_owned`, which frees the superseded buffer
on a grow), and there is no old value to release; on the shared arm it keeps
the clone form, since the alias reads that buffer. The element is lowered by
`lower_append_elem_value`, factored out of the clone form so both store the
same reference with the same retains; i64 / f64 arrays keep the clone form,
whose push op carries the width. The other overrides are still evaluated first,
so `n: a.code.len()` beside `code: a.code.append(b)` reads the length before the
push, as it did. The first cut admitted scalar-element fields only, and the
exit stack then moved to `x86_queue_fixup`'s `fix_names: a.fix_names.append(name)`
— the same clone per fixup, on a `string[]`.

| variant | allocs | frees |
|---|---|---|
| self-host, both updates, in place | 15 | 13 |
| clone-form return update alone, in place | 13 | 11 |
| the fixture (with its `string[]` override) | 200 | 176 |

The sanitized self-built stage1 assembling natively, `-o <binary>`:

| module | before | after |
|---|---|---|
| `lexer.fern` | exit 125 at 13 s | 0.66 s, 95 MB live |
| `parser.fern` | exit 125 at 41 s | 9.3 s, 2.1 GB live |
| `checker.fern` | exit 125 at 38 s | 33 s, 3.6 GB live |

No sanitizer finding on any of the three. What is live at exit is the
compile's own retention (the emit alone leaves 2.96 GB on `checker.fern`), not
the assembler's any more.

## Pinned

`conformance/cases/own_struct_update_reuse`: the helper update, the
return-position update, a clone-form `string[]` override, and an early path
that passes the box on, 8 rounds of 60 instructions read back after churn.
