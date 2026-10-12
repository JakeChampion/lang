# 2026-10-12 — a None field keeps a record static

`ssaunits`: `box_words`, `option_word`; `ir.const_option_ref`.

## The shape

```fern
return HttpResponse { status: 200, body: BodyText(""), headers: HeaderMap { ... }, trailers: HeaderMap { ... }, background: None };
```

A record whose every field is a constant is one static box, built at
compile time and never counted. #12226 gave `HttpResponse` a
`background: Option[() => void]` field, and `ok()` sets it to `None`.
`constants` placed scalar literals, string literals and other placeable
constructions as fields, but a payloadless Option or Result is not a
construction it places: the backends build it as their own static tag
block (`ir.op_const_option`). One `None` field was enough to refuse the
whole record, so every response the serve loop sent was a fresh box, and
`TestSelfHostServeAllocsPerRequest` counted one allocation per request.

## The rule

A field holding a payloadless `None`, or an `Ok`/`Err` of `void`, read
through copies, is the word `{&opt|<tag>}`: the packing of the same static
tag block the variant is on its own. The backends already intern a nested
packing in braces and write its address, so nothing changes below the
plan. The four copies of the copy-following loop in the field-word
functions are `through_copies`, which already existed.

## Measured

The serve loop on x86-64 allocated once per hello request after #12226
and allocates nothing again (144 allocations over 100,000 requests, all
at startup); arm64 and wasm hold the same zero over 10,000.

`TestSelfHostRecordOfStaticBoxesIsStatic` now builds a record with two
`None` fields, one an `Option[() => void]`, and hands it to a recursive
reader so it arrives built; without the rule it allocates on all three
targets.
