# Raw builtin inventory

Status: [reference] inventory for [SEC-003](https://github.com/JakeChampion/lang/issues/13505), step 1 of the enforcement sequence in [the safety contract](../SAFETY-CONTRACT.md).
Parent: [security epic](https://github.com/JakeChampion/lang/issues/13502).

This file lists each builtin that a safe caller cannot use without an obligation.
It records the current access rule for each group.
It does not change any access rule.
The types come from `checker.intrinsic_params`, `checker.intrinsic_result`, and `checker.raw_floor_sigs`.

## Access rules today

| Group | Who can call it today | Checker rule |
|---|---|---|
| Raw floor (`__raw_*`, `__syscall3` to `__syscall6`) | Runtime helper sources only | `raw_allowed` is true only for the bundle check path. Other sources receive `E001`. |
| Memory intrinsics (groups A and B below) | Every source: user programs, the standard library, `coreutils` | `is_resolvable_callee` admits every `__` name. No obligation is recorded. |
| Bounded intrinsics (group C below) | Every source | Same admission. The operations carry a typed value and a length. |
| `unsafe function` declarations | Every source | `E095` refuses a call outside an `unsafe` block, a function value, and an unsafe implementation of a safe trait method. |

The probe `__alloc(16)`, `__store_i32(p, 7)`, `__store_u8(4096 as usize, 1)`, `__load_i32(p)` and `__c_call0(p)` in an ordinary `main` passes both checkers at this commit.
That is the gap that the coordinated enforcement change must close.
The standard library calls group A in `core/map`, `std/jni`, `std/string`, `std/brotli`, `std/deflate`, `std/zstd`, `std/net`, and `std/serve`.
The HTTP and QUIC modules and the TLS record layer call it too.
`coreutils/lib/base.fern` and `coreutils/lib/pwdb.fern` call it too.

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

## Group B: release, count, layout, FFI, and runtime hooks

These take typed values, but they change ownership, layout, or control flow outside the language model.

| Intrinsic | Obligation |
|---|---|
| `__rc_inc(a)`, `__rc_dec(a)`, `__rc_get(a)`, `__fern_rc_dec(p)` | The count must match the real number of references after the call. |
| `__arr_set_len(a, n)` | `n` must not exceed the capacity, and the new elements must be initialized before a read. [The bounds audit](BOUNDS-AUDIT.md) shows a safe `main` that reads past a three-element array through it. |
| `__arr_push_shared_bytes()`, `__arr_push_shared_count()` | Read-only counters. No obligation. |
| `__alloc_bool(n)`, `__alloc_i32(n)`, `__alloc_i64(n)`, `__alloc_u8(n)` | The array is typed, but its elements are not initialized by the language. The initialization audit is SEC-006. |
| `__heap_mark()`, `__heap_release_to(m)` | Every value allocated after the mark must be dead before the release. |
| `__c_call0` to `__c_call4`, with `_f32` and `_f64` forms | A foreign call through a code address. The address and the argument contract are the caller's proof. |
| `__task_new`, `__task_free`, `__task_enter`, `__task_leave`, `__task_park`, `__task_wait`, `__task_set_ready`, `__task_set_cancelled`, `__task_cancelled`, `__task_timeout`, `__task_cur` | Runtime scheduler hooks. A task id must be live and the state transition must be valid. |
| `__syscall3` to `__syscall6` | Arbitrary kernel calls. Admitted only in runtime helper sources today. |

## Group C: bounded intrinsics

These take a typed value together with a length or an index.
They do not forge an address.
The audit of their internal range checks belongs to SEC-005 and SEC-035.

- Scan and count kernels: `__memchr`, `__rmemchr`, `__memchr_bytes`, `__rmemchr_bytes`, `__mismatch`, `__mismatch_bytes`, `__mismatch_bytes_str`, `__count_byte`, `__count_byte_bytes`, `__count_runs`, `__count_runs_bytes`, `__count_below_u64`, `__scan_set`, `__scan_set_bytes`, `__ascii_run`, `__sum_bytes`, `__sum_bytes_array`, `__bsd_sum`, `__bsd_sum_bytes`, `__crc32_cksum`, `__crc32_cksum_array`, `__crc32_ieee_array`, `__str_hash`.
- Byte views: `__bytes_into`, `__bytes_owned`, `__bytes_range`.
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

## Required change

Groups A and B must require an `unsafe` block at every call site.
The standard library, `coreutils`, and the runtime helpers must accept that obligation in annotated blocks.
The compiler imports `std/string`, `std/crypto`, `std/deflate`, `std/http`, and other modules that call group A.
The pinned stage0 compiler, `stage0-20261028-9193717`, parses `unsafe` blocks.
Therefore those modules can carry `unsafe` blocks now, and the enforcement change is one coordinated PR.
