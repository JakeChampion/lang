# 2026-09-21 — a method reads its receiver by name

`@derive(json.Json)` on a struct with an array field synthesises a body that
says `self.items.to_json()`. The typed path refused it with `unsupported call
target: i32[].to_json`, and that refusal took two whole conformance cases with
it.

The receiver is an array, so the call should have gone the way every other
array-method call goes: `register_array_method_generics` folds std/json's
element-poly `(xs: T[]) to_json[T: Json]()` into a free generic
`__arrm_to_json[T]`, and the monomorphiser rewrites `arr.to_json()` into
`__arrm_to_json__i32(arr)`, one clone per element type. A bare local receiver
already took that route: `var nums: i32[] = [10, 20]; nums.to_json()` produced
whole. So did a parameter's field, `b.items.to_json()`. Only a receiver rooted
at `self` did not.

## The fix

`monomorphize_module` seeded its environment from the declaration's
PARAMETERS, and a method's receiver is not one of them. With `self` unbound,
`mono_infer` answered "" for `self.items`, the fold's `type_ends_arr` test
failed, and the call survived to the semantic boundary as a method on `i32[]`
that no contract names.

The generic-struct-method clone site already bound the receiver beside the
parameters, so the shape was there to copy. `env_from_decl` is now that pair,
and the three sites that rewrite a declaration's own body — the whole-module
pass, `ms_func`, and the clone site whose two lines it replaces — share it.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.

| program | before | after | typed held |
|---|---|---|---|
| the four receiver spellings side by side | 0 of 119 | 121 of 121 | 0 B |
| `conformance/cases/derive_json` | 0 of 149 | 150 of 150 | 0 B |
| `conformance/cases/json_array_field` | 0 of 120 | 122 of 122 | 0 B |

Each answers what native answers, and the two lowerings agree.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 825 | 827 |
| declarations produced | 84,094 of 87,227 | 84,367 of 87,231 |

The two programs that become whole are exactly the two the leaf named. Nothing
else changes, and nothing produces less.

## Traps

- **The refusal named the receiver's TYPE, not the receiver.** `unsupported
  call target: i32[].to_json` says the element type was known by the time the
  semantic boundary saw the call, which reads like a missing contract for
  arrays. The contract was never the point: the call should not have reached
  that lookup at all, because an earlier pass was supposed to have rewritten
  it into a free call.
- **Three of the four spellings worked.** A bare local, a parameter's field
  and a parameter itself all produced; only `self`-rooted receivers did not.
  That narrows the fault to what distinguishes a method body from a free
  function's, which is exactly the environment the receiver lives in.
- **The AST leg hid it.** `irlower.lower_array_to_json` admits a scalar-array
  field read explicitly (#6009, after a derived `to_json` serialised the array
  POINTER), so the AST lowering produced the right bytes for the same source
  and the gap showed only where the typed path had no such arm. That guard
  still covers receivers the fold cannot infer — a generic struct's field whose
  type mentions a variable — so it stays.
