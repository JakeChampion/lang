# 2026-10-01 — a primitive boxed into dyn (#10908)

Self-host (`ssasem.gathers_views`, `semsource.dyn_impl_names`), the self-host
checker (`type_assignable`, `conforms_to_dyn_trait`, `redeclares`) and the
native checker's struct-field and variant-payload dyn sites.

Two refusals of the typed lowering, each failing the compile under
`FERN_SEM_IR_STRICT`.

## A box of a counted string returned as a view-holding dyn

`function pick(s: string): dyn Size { var t: string = s + "!"; return t; }`,
where another concrete of `dyn Size` holds a `str`, was refused as "view
result escapes its source". `dyn_box` is a construction everywhere else
(`ssaunits.construction`, `ssarc`), but `gathers_views` left it out, so
`view_sources` fell through to "escapes" and `bytes_roots` answered the box
itself. A dyn box now gathers its operand's views like any construction: a
box of a string or a scalar reads no bytes but its own, and anchors nothing.

## A `str` boxed into a dyn

`d = v` with `v: str` and `d: dyn Size` was refused as "replacement type does
not match its binding". The refusal is right: native's `assignable` assigns a
`str` only to a `str`, so it rejects the program at every coercion site (var,
assignment, argument, array element, return). The box would hold the view
past its source, and the AST lowering accepted `return v;` from a
`dyn Size` function over a local's bytes. The self-host checker's dyn arm of
`type_assignable` accepted any source, and now rejects a `str`.

Found on the way, each with a codes-differential row:

- **An impl on `str` is the impl on `string`.** Native keys an impl by
  `methodTypeName`, which names a `str` receiver `string`. The self-host
  checker compared the unerased name, so `impl Size for str` beside
  `impl Size for string` was accepted (both dispatched to the `str` body: the
  AST lowering printed `106` for a `string` whose impl answers `1006`), and a
  `string` into `dyn Size` through `impl Size for str` was E003. Both now go
  through `parser.receiver_base`. `semsource.dyn_impl_names` takes the same
  erasure, so such a dyn lists the `string` box among its concretes; it
  listed nothing, so its release walked no string box.
- **Native admitted a `str` in a dyn struct field or variant payload.** Those
  two sites hand-copied `assignable`'s dyn branch without its `str` rule. They
  now ask `assignable`.

## Measured (x86-64 sanitize, allocs / frees)

| program | before | after |
|---|---|---|
| boxed string returned as a view-holding dyn | refused | 170 / 170 |
| string boxed through `impl Size for str`, returned and merged | E002 / E003 (self-host checker) | 490 / 490 |

Each answers as the AST lowering does on x86-64, arm64 and wasm.

## Tests

`TestSelfHostSemanticProduction`:

- `a-boxed-string-returned-as-a-view-holding-dyn-is-produced`
- `a-string-boxed-through-an-impl-for-str-is-produced`

`TestSelfHostCheckerCodesX86_64`: `str-and-string-impl-redeclared`,
`string-into-dyn-through-str-impl`, and `str-into-dyn-` at a var, an
assignment, an argument, a return, an array element, a field and a payload.
`TestDynTraitChecker`: a `str` in a dyn struct field and a variant payload.
