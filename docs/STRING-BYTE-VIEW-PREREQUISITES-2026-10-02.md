# Byte-view representation prerequisites

These changes prepare the primary compiler for non-allocating `as_bytes()`
views in #5626 / #5632. Conversion still copies at this checkpoint.

The checker now reserves built-in `append` and `with` for owned arrays.
Read-only views report E043 for those missing methods, without a secondary
E055 discard diagnostic. User-defined methods on view receivers remain valid,
including methods named `append` or `with`. The semantic verifier independently
rejects array mutation operations whose receiver is a view.

WASM string literals and per-module string-region boundaries are aligned to
four bytes. The same padding rule applies to relative and linked offsets.
The runtime cache key changes because old cached units contain old offsets.

Validation used a frozen source snapshot and a follow-up snapshot correcting
the E055 diagnostic. The full checker corpus exposed that diagnostic error;
the affected cases, lambda variants and discard controls passed after repair.
Semantic, production ownership and allocation tests passed. WASM alignment,
constant aggregate, per-module link and cache tests passed, as did all lint
gates. The prior anchor checkpoint had passed the full unit suite.

An actual primary-compiler cache probe used two modules: the new emitter missed
both old entries, matched clean output, then reused its own entries. Twenty
Darwin/WASM byte-view runtime cases retained balanced allocation/free counts.
Custom view methods also compiled and executed with the primary compiler.

The pinned Darwin bootstrap reached identical stage 2 and stage 3 binaries:
12,990,145 bytes, SHA-256
`039fb2ee864414734ddef432570ec16ab9cb9d335c3a389c423fba85a9257eb9`.
With the same saved primary compiler generating both sources for x86-64 Linux,
the compiler grew from 11,460,800 to 11,461,688 bytes. The 888-byte increase
includes the checker restrictions and alignment calculation. Loaded memory
grew by the same amount, from 12,550,696 to 12,551,584 bytes. No size baseline
was changed, and these measurements make no runtime performance claim.
