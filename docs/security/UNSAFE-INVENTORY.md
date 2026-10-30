# Raw builtin inventory

Status: [reference] inventory for [SEC-003](https://github.com/JakeChampion/lang/issues/13505), step 1 of the enforcement sequence in [the safety contract](../SAFETY-CONTRACT.md).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

This file lists each builtin that a safe caller cannot use without an obligation.
It records the access rule for each group.
The types come from `checker.intrinsic_params`, `checker.intrinsic_result`, and `checker.raw_floor_sigs`.

## Access rules today

| Group | Who can call it today | Checker rule |
|---|---|---|
| Raw floor (`__raw_*`, `__syscall3` to `__syscall6`) | Runtime helper sources only | `raw_allowed` is true only for the bundle check path. Other sources receive `E001`. |
| Memory intrinsics (groups A and B below) | Every source, inside an `unsafe` block | Both checkers refuse a call outside an `unsafe` block as `E095`. The bundle check path is an unsafe context, because the runtime helper sources are the raw floor. |
| Bounded intrinsics (group C below) | Every source | `is_resolvable_callee` admits them. The operations carry a typed value and a length. |
| `unsafe function` declarations | Every source | `E095` refuses a call outside an `unsafe` block, a function value, and an unsafe implementation of a safe trait method. |
| Unsafe built-in methods (below) | Every source, inside an `unsafe` block | `E095` refuses a call outside an `unsafe` block. |

The set is `unsafe_intrinsics` in `compiler/checker.fern` and `UnsafeIntrinsics` in `internal/check/checker`.
`TestUnsafeIntrinsicsMatchSelfHost` pins the two equal.
The probe `__alloc(16)`, `__store_i32(p, 7)`, `__load_i32(p)` and `__free(p, 16)` in an ordinary `main` is `conformance/cases/diag_e095_raw_builtin_outside_unsafe`, and the same calls inside a block are the control `unsafe_block_accepts_raw_builtins`.

The standard library carries the blocks in `core/map`, `std/jni`, `std/async`, `std/string`, `std/brotli`, `std/deflate`, `std/zstd`, `std/net`, `std/serve` and `std/sim`.
The HTTP and QUIC modules, the TLS record layer and the AEAD modules carry them too.
`coreutils/lib/base.fern` and `coreutils/lib/pwdb.fern` carry them too.
A block wraps the body of each function that calls a raw builtin, so the function's own validation runs inside it.

## Group A: address-carrying intrinsics

Each one takes or returns a `usize` address.
The caller must prove that the address is valid, aligned, owned, and in range for every access.

| Intrinsic | Signature | Obligation |
|---|---|---|
| `__alloc(n)` | `i32 -> usize` | The size must not wrap. The block is uninitialized. |
| `__alloc_reuse(p, old, new)` | `usize, i32, i32 -> usize` | `p` must be a live block of size `old`. |
| `__free(p, n)` | `usize, i32 -> void` | `p` must be a live block of size `n` with no other reference. |
| `__load_u8(p)`, `__load_i32(p)`, `__load_i64(p)`, `__load_ptr(p)` | `usize -> value` | `p` must point at initialized memory of the loaded width. |
| `__store_u8(p, v)`, `__store_i32(p, v)`, `__store_i64(p, v)`, `__store_ptr(p, v)` | `usize, value -> void` | `p` must point at writable memory that no live value aliases. |
| `__memcpy(d, s, n)`, `__memset(d, v, n)` | `usize, usize or i32, i32 -> void` | Both ranges must be valid for `n` bytes and must not overlap. |
| `__str_bytes(s, p)` | `string, usize -> usize` | `p` must have room for the string's bytes. |
| `__account_entry(k, p)` | `i32, usize -> usize` | `p` must be an allocator block header. |
| `__fern_rc_inc(p)`, `__fern_arr_dec(p, n)`, `__fern_box_free(p, n)`, `__fern_drop_arr_ptr(p, n)`, `__fern_str_dec(s)` | box pointer and count | `p` must be a live box with a correct count. A wrong count double-frees or leaks. |

The Go oracle runs `__alloc`, `__free`, the loads, the stores, `__memcpy` and `__memset` on a byte arena (`internal/oracle/interp/rawmem.go`).
The arena checks each obligation above, and a broken obligation stops the run with an error.
The arena holds only `__alloc` blocks, so it cannot read the layout of a value.
Thus the oracle does not run `__alloc_reuse`, `__str_bytes`, `__account_entry` or the box helpers.
`fern -interp` sends these calls to the runtime of its own process, and it does no checks.

## Group B: release, count, layout, FFI, and runtime hooks

These take typed values, but they change ownership, layout, or control flow outside the language model.

| Intrinsic | Obligation |
|---|---|
| `__rc_inc(a)`, `__rc_dec(a)`, `__rc_get(a)`, `__fern_rc_dec(p)` | The count must match the real number of references after the call. |
| `__arr_set_len(a, n)` | `n` must not exceed the capacity, and the new elements must be initialized before a read. [The bounds audit](BOUNDS-AUDIT.md) shows a safe `main` that reads past a three-element array through it. |
| `__arr_push_shared_bytes()`, `__arr_push_shared_count()` | Read-only counters. No obligation. |
| `__heap_mark()`, `__heap_release_to(m)` | Every value allocated after the mark must be dead before the release. |
| `__c_call0` to `__c_call4`, with `_f32` and `_f64` forms | A foreign call through a code address. The address and the argument contract are the caller's proof. |
| `__task_new`, `__task_free`, `__task_enter`, `__task_leave`, `__task_park`, `__task_wait`, `__task_set_ready`, `__task_set_cancelled`, `__task_cancelled`, `__task_timeout`, `__task_cur` | Runtime scheduler hooks. A task id must be live and the state transition must be valid. |
| `__syscall3` to `__syscall6` | Arbitrary kernel calls. Admitted only in runtime helper sources today. |

## Unsafe built-in methods

| Method | Signature | Obligation |
|---|---|---|
| `close_unchecked()` on `Reader`, `Writer` and `Dir` | `-> Result[void, IoError]` | The call closes the handle through a borrow. No copy of the handle can be used after the call, and no other copy can be closed. |

SEC-042 makes `close` consume its handle.
The interpreter in `compiler/interp.fern` keeps each handle in a shared value, so it cannot give that value to `close`.
`close_unchecked` closes the handle and leaves the shared value in place.
It lowers as `close`.

The set is `unsafe_builtin_methods` in `compiler/checker.fern` and `UnsafeBuiltinMethods` in `internal/check/checker`.
`conformance/cases/handle_close_unchecked_through_a_borrow` closes each kind of handle inside a block.
`conformance/cases/diag_e095_close_unchecked_outside_unsafe` is the refusal.

## Group C: bounded intrinsics

These take a typed value together with a length or an index.
They do not forge an address.
The audit of their internal range checks belongs to SEC-005 and SEC-035.

- Scan and count kernels: `__memchr`, `__rmemchr`, `__memchr_bytes`, `__rmemchr_bytes`, `__mismatch`, `__mismatch_bytes`, `__mismatch_bytes_str`, `__count_byte`, `__count_byte_bytes`, `__count_runs`, `__count_runs_bytes`, `__count_below_u64`, `__scan_set`, `__scan_set_bytes`, `__ascii_run`, `__sum_bytes`, `__sum_bytes_array`, `__bsd_sum`, `__bsd_sum_bytes`, `__crc32_cksum`, `__crc32_cksum_array`, `__crc32_ieee_array`, `__str_hash`.
- Byte views: `__bytes_into`, `__bytes_owned`, `__bytes_range`.
- Typed allocations: `__alloc_bool`, `__alloc_i32`, `__alloc_i64`, `__alloc_u8`. Every payload word is zero at allocation ([ERASURE-AND-QUOTAS.md](ERASURE-AND-QUOTAS.md)), so a fresh array carries no obligation.
- Arithmetic and text conversion: `__abs_f64`, `__ceil_f64`, `__floor_f64`, `__round_f64`, `__sqrt_f64`, `__trunc_f64`, `__scale_f64`, `__outer_mul_f64`, `__inner_mul_add_f64`, `__clz32`, `__clz64`, `__ctz32`, `__ctz64`, `__popcount32`, `__popcount64`, `__mulhi_u64`, `__ptr_width`, the `__primitive_*_to_string` family.
- Lanes: the `__f64x2_*` and `__f32x4_*` families.
- Cryptographic kernels: `__aes_expand_key`, `__aes_ctr32`, `__aes_gcm_seal_into`, `__aes_gcm_open_into`, `__ghash`, `__sha1_hw`, `__sha1_hw_blocks`, `__sha256_hw`, `__sha256_hw_blocks`. Their buffer contracts belong to SEC-037.
- Diagnostics: `__ct_secret`, `__ct_public`, `__heap_alloc_count`, `__heap_bump_bytes`, `__rc_underflow_count`, `__map_hash_seed`.

## Raw floor

[RUNTIME-INTRINSICS.md](../RUNTIME-INTRINSICS.md) documents each `__raw_*` intrinsic and the syscall sub-floor.
`__raw_string` requires an unadjusted `__raw_alloc` result.
`TestRawStringPointerComesFromRawAlloc` holds that rule.
The floor is private to the runtime helper sources.
A user program or a standard library module that names one receives `E001`.

## Remaining limits

- A block wraps a whole function body. A finer block per raw operation, with the validation outside it, is a per-function refactor the contract's step 4 still describes.
- The runtime helper sources carry no blocks. Their check path admits the raw floor, and the same path is the unsafe context.
- A call through a function value carries no obligation, as for an unsafe function. A raw builtin is not a value, so no such path exists for it.
