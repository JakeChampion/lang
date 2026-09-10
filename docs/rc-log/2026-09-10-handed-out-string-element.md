# A caller-owned string element handed to a callee that keeps it, and a closure array's field spelling

Refs #9023, #9017; follows `2026-09-10-spread-carry-elems-secured.md` (#9014,
#9021). The regressions of 59e40b4 that #9021 did not reach.

## What was red

Main from 54ce1bf, after #9021 landed:

| check | symptom |
| --- | --- |
| `test-e2e-selfhost-x86_64` shard 5, `TestSelfHostCheckerDifferentialX86_64/loop-map-shadow` | the self-host-built checker dies on a signal binding `for (k, v) in m` |

## The handed-out element (#9023)

Under `FERN_SANITIZE=1` the checker reports a use-after-free: `__fern_arr_inc_elems`
inside `Scope.bind` walks a `names` buffer whose element `"k"` `__fern_str_arr_free`
freed at the exit of `for_binding`. A hardware watchpoint on the block's header
found the free; the raw stack walk found the walker.

`for_binding` splits the pattern into a fresh `string[]` and hands it to
`bind_tuple_destr_names`, whose parameter is box-borrowable, so the caller keeps
its deep free. Inside, `var nm: string = names[i]` reads an element that deep
free releases, and `out.bind(nm, et)` hands it to `bind`'s `name`, a parameter
that is stored (`s.names.append(name)`) and so neither borrowable nor counted.
A string parameter at such a position takes over the argument's reference; the
element had none to give, so the scope stored it uncounted. The deficit is older
than 59e40b4: a sixty-line reproducer of the shape leaks at afc6d0a and runs on
freed-but-intact memory, and the element retain on the un-share copy is what
made it fault.

Main's 8e9bf1a stops the walk instead: a `string[]` is no longer a
counted-element array, so an un-share copy shares its elements uncounted as
it did before #9014, and the string[] field's release stays refused per type
(#5338's class). That leaves the element dangling in the scope once the
caller frees it, as it was at afc6d0a; the count below is what keeps it
alive, and the two compose.

`retain_caller_elem_handoff` closes it at the handoff: a `str_param_elem_escapes`
argument (a `p[i]` of a borrowed `string[]` parameter, or a local such a read
bound) is retained at every call-argument site whose position is neither
borrowable nor `CNT:`-counted in the frame's registry, the same second owner
`xs.append(p[i])` takes inside one function. A position that keeps the value
for another reason leaks one count rather than freeing under a holder.

With the binding's name intact, `loop-map-pair-types` (`return k.len() + v`,
`v: i64`) then showed the checker typing a settled `i32 + i64` as `i32`:
`int_result` returned the left side where native's `commonIntegerWidth` widens
to the wider operand in either order. It now does too; a literal tree on either
side still reads at the other operand's width, so `a + 4611686018427387904`
against an `i32` stays E047 rather than becoming an `i64` (#8722). The case had
passed on 54ce1bf by accident: the binding's name was read from a recycled
block and resolved to nothing.

## The closure array's field spelling (#9017)

#9021 refuses a closure array's SLOT in `arr_expr_counted_elems`. A struct field
of that type is spelled `fn[]`, and `is_enum_array_field_type` admits it, so the
value forms on `h.hs` and the in-place field forms still followed the clone with
`__fern_arr_inc_elems`, writing an rc word eight bytes before a lambda's entry.
Whether that faults depends on the bytes that happen to sit there. The exclusion
sits in `is_counted_elem_array_type`, the one predicate all four sites ask.

## Gates

- `TestSelfHostStrElemHandoffX86_64`: the reproducer, pinned on the one retain
  in the handing function and run under the sanitizer against the interpreter's
  exit (a use-after-free on main). The checker differential gains five
  mixed-width cases, both operand orders at both return widths.
- `TestSelfHostKeptCallHolderX86_64`: the closure array as a local and as a
  struct field, pinned on the emit (`round` carries no `__fern_arr_inc_elems`;
  the field row fails on main) and run under the sanitizer; and the #9016
  holder handed directly and through an alias, run under the sanitizer, which
  #9021's counted handback keeps sound.
- `TestSelfHostCheckerDifferentialX86_64` green (red on main at
  `loop-map-shadow`); `TestSelfHostConstFuncGen2`, `TestSelfHostSpreadCarryElems*`,
  `TestSelfHostPerModuleEmitAllFixpointX86_64`, `make lint-all` and the
  complexity ratchet green on the final tree.
