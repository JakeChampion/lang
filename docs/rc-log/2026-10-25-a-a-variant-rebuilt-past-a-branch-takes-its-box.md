# 2026-10-25 — a variant rebuilt past a branch takes its box

`ssaunits.dying_unclaimed`, replacing `dying_after_claims` and `last_claim`;
`ssaunits.token_busy`, `carried_builds` and `token_slot_held`;
`ssarc.block_body`, `reuse_construct` and `slot_token`.

## The shape

`std/serve.__serve_send` takes the TLS session out of its connection slot,
writes through it, and stores a new `Secured` back once the write succeeds:

```fern
match (c.tls[at]) {
  Secured(sess) => {
    c = __Conns { ...c, tls: c.tls.with(at, Plain) };
    match (sess.write(payload)) {
      Ok(step) => {
        return __serve_send_raw(drv, __Conns { ...c, tls: c.tls.with(at, Secured(step.session)) }, at, step.output, opts);
      },
      Err(_) => { return (c, Failed); }
    }
  },
  ...
```

The element `c.tls[at]` is taken and its payload handed to `write`, so the
element's two-slot box dies in the arm's first block. The `Secured(...)`
construction that could reuse it is in a later block, past the branch on the
call's result. `carried_pairs` only considered values dying after the last
construction of any size in their block, and the arm's first block ends with
the three-slot `__Conns { ... }` update, so the element was freed there and
`Secured` allocated.

## The change

A value is a carry candidate when no construction of its own slot count
follows it in its block (`dying_unclaimed`, `claimed_from`). A construction
of another size after it cannot take its token anyway, so nothing that paired
in the block before pairs differently now.

A carried token now lands in blocks where a keep holds the function's one
token slot. In `__serve_send` the block that builds `Secured` also rebuilds
the 39-field `__Conns`, whose keep starts at the `tls` field's read, before
`Secured`. The carried construction copied its token into the slot
(`self_token`), so `token_busy` refused the keep: the update retained all 38
fields, dropped the old record and reused its shell, 750 instructions a
request more. A carried construction inside a keep's or a classified donor's
span now reads its token from the donor's own local, where the carry already
holds it, and leaves the slot alone, so `token_busy` no longer counts a
carried build. Everywhere else it moves the token into the slot as before
(`slot_token`).

The first version read every carried token from the donor's local. That
changed the lowered shape of every carried construction, and the storage
proofs (`ssacreditreuse.check_invocations`, run by
`TestSelfHostCheckedStorageInvocation` and `TestSelfHostRetainedNativeBodies`)
refused the shape (`invalid reuse boundary`). Their drivers then exited
mid-run (94 and 89), and the leak checker reported every block they still
held: a leak in the census, not in any compiled program. The protocol the
proofs check is the slot's, so only the case the slot cannot serve leaves
it.

## Measured

- The probe, `variant-rebuilt-past-a-branch` in `TestSelfHostPayloadTakeIR`
  (1,000 calls): 1,003 allocations to 4, on x86-64, arm64 and wasm.
- `TestSelfHostServeAllocsPerRequestTLS`: 25.01 to 24.01 per request (the
  `Secured` box in `__serve_send`), 18,678 to 18,596 instructions. With the
  carry and without the slot change it read 19,421.

## Traps

- `TestSelfHostCrossBlockReuse` runs with the inliner off (`boxedProbes`).
  The same probe there still reads 1,004: without the splices the arm's
  reads are not where the plan can take them. That suite is the wrong home
  for this shape.
- `-o x.wasm` builds a component, and wasmtime reports any nonzero exit of a
  component as 1. A case meant for the three targets has to return 0, or the
  harness's `-emit asm` core module, which keeps the code.
