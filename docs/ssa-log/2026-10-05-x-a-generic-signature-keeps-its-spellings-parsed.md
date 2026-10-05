# 2026-10-05 — a generic signature keeps its spellings parsed

Self-host checker, every target. Refs #11534, #8171.

## The shape

A call to a generic function binds the callee's type variables against the
argument types (`arg_bindings`, `gc_bind_param`). That needs each parameter
spelling, and often the return spelling, parsed into a `parser.TypeRef`. The
checker parsed them again at every call:

- `arg_bindings`, 24,000 calls compiling `checker.fern`, about 2,100
  instructions a parse;
- `sig_call_type`, `written_type_arg_diags`, `uninferred_call_diags`,
  `dest_bound_diags`, `fn_value_binds`, `bound_param_dest`,
  `use_callback_slot`, `lit_bound_typevar` and the `gcall_*` pair.

A function-typed parameter's spelling is the expensive one. Its spelling,
`(T) => boolean` and the like, is composed when the signature is built
(`fn_tag_spelling`), so no memo of the module's written spellings holds it.

`FuncSig.refs` now holds a generic signature's parameter spellings parsed,
then its return spelling's, built once in `collect_func_sigs`. `sig_param_ref`
and `sig_ret_ref` read it, and parse as before for a signature that is not
generic, which the unknown-return path of `sig_call_type` can still reach.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at f22d73e9 against this branch. The two compilers build
`checker.fern` for x86-64 and arm64, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | parsed once |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 19.010 G | 18.942 G (−0.36%) |
| `arg_bindings`, inclusive | 135 M | 80 M |
| `parser.parse_type_ref`, inclusive | 204 M | 121 M |
| `sig_call_type`, inclusive | 276 M | 248 M |
| `generic_sig_refs` | — | 1.4 M |
