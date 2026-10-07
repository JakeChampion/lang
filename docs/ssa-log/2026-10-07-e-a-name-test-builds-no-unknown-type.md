# 2026-10-07 — a name test builds no unknown type

`checker.Scope.lookup`. Refs #8171. No emitted byte changes.

## What changed

`Scope.lookup` answers a miss with `t_unknown("undefined identifier:" +
name)`. Each miss concatenates a string and boxes a type, and the caller
then drops both. On a `checker.fern` compile that came to 134 k misses.
Most come from nine sites that only test the answer,
`is_unknown(s.lookup(name))`, to ask whether a name is a known value:

- `ctor_name_taken`;
- `qual_enum_name`;
- `assoc_call_req`;
- the generic-function-value tests;
- the variant-ambiguity test.

The names they ask about are mostly constructors, enum qualifiers and
functions, which are not values, so most of their lookups miss.

`Scope.known(name)` answers the same question from the binding index alone,
and the nine sites call it. A miss builds nothing.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built by its own stage 2.
The baseline is main at ec2399635. Both stage 3s rebuild themselves byte for
byte. The two compile `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, to the same bytes.

| | before | this change |
|---|--:|--:|
| total Ir | 15.266 G | 15.235 G (−0.21%) |
| stage 3 size | 10,791,880 | 10,791,824 |
