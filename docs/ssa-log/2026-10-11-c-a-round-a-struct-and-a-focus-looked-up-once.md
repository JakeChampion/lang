# 2026-10-11 — a round, a struct and a focus, looked up once

`seminline.splice_round`, `checker.find_variant_sig` / `find_struct_sig`,
`checker.in_focus`. Refs #8171. No emitted byte changes.

## What changed

- **A splice round skips the bodies that call no ready function.** Every
  round handed every body to `splice_calls`, which walked all its
  instructions looking up each call's callee by name. That was 137,867 calls
  and 1.94 G Ir on the whole compiler, for 9,421 splices. Each body's direct
  callees now sit beside it, recomputed where a splice rewrote one. A body
  none of whose callees is ready is one `splice_calls` would leave as it is,
  since that mode splices only direct calls to ready functions.
- **Variant and struct signatures resolve through the struct table's name
  chain.** `find_variant_sig` and `find_struct_sig` scanned the whole
  `StructSig` list. The chain is ascending, so the first match is the
  declaration the scan found. The literal, match and variant-arity walks
  that reach them pass the `StructTable` down instead of its list.
- **A check pass looks its focus up in a `NameIndex`.** `in_focus` asked
  `util.has_str` of the focus list once per function per walk. The re-check
  over a clean module's clones names every clone, so the list is long.

## Measured

The whole compiler (`fern.fern` to an x86-64 binary) under callgrind. The
four compilers each compiled the same frozen copy of the tree, on a warm
memo of their own. Each row is measured against the one above it.

| | Ir |
|---|--:|
| main at 7d0378c | 73.753 G |
| splice rounds skip what they cannot change | 73.288 G (−0.63%) |
| struct signatures through the name chain | 72.750 G (−0.73%) |
| the focus in a name index | 72.575 G (−0.24%) |

Together −1.60%. Each change compiles `checker.fern` to the same bytes for
x86-64, arm64 and wasm, and the changed compiler rebuilds itself byte for
byte.

## Trap

A first measurement of the middle row compiled the tree while the third
change was half-written. It failed type-checking a fifth of the way in, and
reported 14.5 G. Measure a frozen copy of the sources, never the working
tree you are editing.
