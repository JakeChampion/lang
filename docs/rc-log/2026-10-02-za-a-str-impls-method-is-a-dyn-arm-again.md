# 2026-10-02 — a `str` impl's method is a dyn arm again

Self-host (`semsource.dyn_arms`).

`2026-10-01-b-a-primitive-boxed-into-dyn.md` sent the self-host checker and
`semsource.dyn_impl_names` through `parser.receiver_base`, which spells a `str`
receiver `string`. `dyn_arms` was a third site and kept
`util.base_type_name`, which leaves `str` as `str`. It then asked
`implements_traits`, which keys the impl list with `receiver_base`. So an
`impl T for str` never implemented `T` as far as the arm search saw, and its
method was never an arm.

That was latent until #11122 made the backends dispatch over exactly the arms
`dyn_arms` names, instead of every method with the same name. cec5cefc (#11138)
made the arm search key the receiver with `receiver_base`, as
`implements_traits` and `decl_key` do, and 58869434 (#11155) added the
single-implementer case.

## The trap: the two shapes fail at different layers

- **One implementer.** The arm list comes back empty while `any_implementer`
  (also keyed by `receiver_base`) says the trait has one. So `dyn_method`
  refuses at compile time: "dyn method has no implementation".
- **Two implementers.** The arm list holds only the other impl's method. The
  compile succeeds, and a string box reaches the dispatch chain's fallthrough
  at run time, which exits 134 with no output.

## Measured (x86-64)

| program | before | after |
|---|---|---|
| dyn whose only implementer is `impl T for str` | refused | `1043 1030` |
| string boxed through `impl T for str`, merged past a branch-local source | exit 134, no stdout | `1050 1043 1050 26` |

Both agree with the native compiler.

## Tests

`TestSelfHostSemanticProduction`:

- `a-dyn-whose-only-implementer-is-str-is-produced` (refused before)
- `a-string-boxed-through-an-impl-for-str-is-produced` (exit 134 before)
