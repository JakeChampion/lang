# 2026-10-25 — an ownership row holds the final mode

`semsource.mode_bits`, used for the seed and every update in `infer_rows`.

## The shape

`std/tls/record.__seal(p: Protection, ...)` reads `p.key` into
`chacha20poly1305.seal(key: [u8], ...)` and `p.gcm`'s key into `Key.seal`.
Neither callee counts those parameters: a view, or an array, is never
promoted (`owned_shape`). But the callee's row kept its raw escape bits, and
`chacha20poly1305.seal`'s settled at `1101`. `call_carried` reads the row,
so `__seal` counted its argument as consumed, carried that to `p`, and
inferred `p` COUNTED. Every caller of `__seal` then retained the protection
for a call that only borrows it.

## The change

The row's bit for a parameter is now `inferred_mode(...) == counted`, the
mode the parameter ends with, from the first seed on. A caller reading the
row sees what the callee's contract will say.

## Measured

- `TestSelfHostOwnershipInference`'s modes program gains `sealed_len`, a
  record whose field only reaches `chacha20poly1305.seal`'s key: COUNTED
  before (it calls `__sem_release_Sealer`), borrowed after.
- TLS hello on x86-64 (callgrind, the slope from 2,000 to 6,000 requests):
  18,748 to 18,569 instructions per request; allocations unchanged at 22,
  since the planner's runtime uniqueness test already let the protection's
  rebuild reuse its box once the retain was released.
