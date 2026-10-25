# 2026-10-25 — a two-word payload returns in three words

`sempair.split_parts`, `payloads_whole`, `parts_of`; `ssasem.pair_capture_imm`;
`ir.op_ret_pair_word` / `op_call_pair_word`; `rccertify.Abi.split_second` /
`split_third`. Refs #13169.

## The shape

```fern
pub function (p: Protection) seal(...): Result[Sealed, RecordError] {
  ...
  return Ok(Sealed { record: out, next: __advanced(p) });
}
```

The two-word return carried the `Result`'s position and one payload word. A
`Sealed` fits that word only as a pointer to its box, so the record was built
on every call even though every caller only read its two fields.

## The change

A variant whose payload is a pair, or a record of two fields, each of which
fits a word, returns its position and the two parts. The second part goes in
a second pair word: `%rcx` on x86-64, x2 on arm64, `$__fern_ret_word1` on
wasm. Callers capture both words at the call and rebuild the payload as a
construction inside the variant's, which split takes apart.

A function takes this form only while every caller takes the payload apart
too. A caller that keeps it whole leaves the function in the two-word form,
so the box is never moved from a callee that built it for free into a caller
that cannot.

The certifier reads each pair word's mask from the declared type. A call that
captures two words reads the first word's mask from the split form, where a
scalar field holds no unit.

## Measured

`TestSelfHostPairReturn`'s `split` program, 20 rounds of six calls:

| Build | Allocations in the loop |
|---|---:|
| base (`tls-hello-allocs`, 1a6a93489) | 130 |
| This change | 0 |

The three backends agree on the value, and every block is freed.

Serve hello gates, x86-64. Allocations are per request over 20,000 requests
on one connection. Instructions are a callgrind slope from 2,000 to 6,000
requests.

| Gate | Allocations | Instructions |
|---|---|---|
| TLS | 22.00 to 16.00 | 18,587 to 18,024 |
| HTTP/1.1 | 0.00 to 0.00 | 8,976 to 8,970 |
| HTTP/2 | 20.00 to 20.00 | 11,813 to 11,806 |
| HTTP/3 | 190.35 to 190.35 | 85,753 to 85,579 |

The six TLS boxes are `Sealed`, `Wrote`, `ServerRun` twice, `Absorbed` and
`Deframed`. The TLS ceiling moves from 23 to 17.

Of the x86-64 perf rows, only `deflate_decode` moves: `.text` from 12,108 to
12,112 and `.ir` from 78,659,300 to 78,657,892. `map_string` emits identical
assembly on both builds and moves only with its hash seed. The aarch64 rows
were not measured locally.

## Next

`record.Opened` has three fields, and `server.Step` / `handshake.ServerStep`
have four and five. They are the same pattern one size up, and they are most
of the TLS count that remains, with the byte buffers of #13170.
