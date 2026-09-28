# Producer elements earn the struct-array walk

An array of structs with an array field, built from calls to a strict-fresh
struct producer, freed only its buffer under the AST lowering (#10582):

```fern
hs = hs.append(hold([i]));            // hold returns H { xs: xs }
var hs: H[] = [hold([i]), hold([i, 1])];
```

## Cause

The ARRSTRUCT element walk (`__struct_drop_<T>` per element, then the buffer)
is granted only when every element is one the array may own.
`arrstruct_self_store_elem` and `arrstruct_lit_is_fresh` admitted a fresh
struct literal or a bare ident, never a call. The producer registry already
proves such a call's box is rc 1 and unaliased: `producer_elem_is_fresh`
admits it for producers, and the scalar-field sibling `structarr_elem_store_ok`
admits it on the store side. The deep class was the one that did not ask.

`hold` itself was already registered. `H { xs: xs }` over a parameter is
strict-fresh because `H` routes field reclaim, and a semantic-lowered `hold`
writes the bare row through `ssarc.caller_rows` (`built_record`).

## Change

- Both element rules now ask `producer_elem_is_fresh`. The unshadowed
  producer list is computed once in `arrstruct_credit_rows` and threaded to the
  credit, the unsafe gate and the element-payload escape scan.
- `arrstruct_owned_elem_sites` stamps bare-ident elements only, which
  `arrstruct_self_store_elem` always admits. It now reads the store index
  directly and drops the four parameters it no longer needs.
- The escape scan reads a field chain `ps[i].inner.xs[j]`,
  `ps[i].inner.k` and `ps[i].inner.xs.len()` as a borrow, like the one-link
  `ps[i].xs[j]`. Before, the first link's struct-typed field counted as a
  bare extract, so any nested read refused the credit, for literal elements
  too. A bare `ps[i].inner` or `ps[i].inner.xs` still escapes, and a link that
  does not resolve to a struct field refuses.

## Measured (allocs / frees, x86-64 leakcheck; wasm counts agree)

| row (`TestSelfHostArrStructProducerElem`) | lowering | before | after |
|---|---|---|---|
| `arrfield_append` (the issue) | `ast`, `ast_main`, `ast_callees` | 12 / 2 | 12 / 12 |
| `arrfield_literal` | `ast`, `ast_main`, `ast_callees` | 25 / 5 | 25 / 25 |
| `nested_append` | `ast`, `ast_main`, `ast_callees` | 39 / 3 | 39 / 39 |
| `nested_deep_read` | `ast`, `ast_main`, `ast_callees` | 60 / 6 | 60 / 60 |

The semantic lowering balanced every row before and after. `strfield_append`
(a scalar-field struct with a `string` field) balanced before too: that class
already admitted producer calls.

The guard rows are unchanged and run with no sanitizer finding other than the
leak: `guard_shared_producer` (8 / 4, a producer returning `src[k]`),
`guard_elem_returned` (24 / 6 on `ast`, 24 / 18 on `ast_main`),
`guard_elem_kept` (26 / 13) and `guard_nested_field_kept` (45 / 7). Each is
the refusal floor: an element or nested field outlives the array, or the
producer is not strict-fresh.

## Still leaking

- An element that outlives its array (`keep = hs[i]`, `return hs[i]`) costs
  the whole array its walk under the AST lowering, for literal and producer
  elements alike (`guard_elem_kept` measures the same 26 / 13 with
  `H { xs: [i, r] }` elements).
