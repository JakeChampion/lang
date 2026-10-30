# Preserve word borrows through semantic splices

A buffer address read from a record can depend on the lifetime of that record. A call keeps its record argument alive until the call returns. A semantic splice can make the field read the last use of the record. Its finalizer then runs before a later call reads the buffer.

Check this dependency at `splice_calls`, before either splice path. The late `grown_tiny` check remains. A function with one call site and a forced `@inline` function must obey the same lifetime condition.

Follow the word through casts and known getter results. Compute return summaries to a fixed point over the current function table. A known numeric parser does not return a borrowed word just because it has a reference argument. Use a conservative rule for an unknown callee or a call through a function value. A declaration without a body remains unknown even when the name index contains it.

A byte or boolean cannot contain a complete buffer address on a supported target. These types do not start or propagate a full word dependency. Wider scalars and reference containers can carry the dependency. The unchanged `ip_parse` allocation test checks that numeric byte and group conversions remain eligible for a splice.

Pair operations transfer their input dependency. A normal pair transport operation does not create a new borrow. The return summary also checks pair payload stores. The function can return a constant tag through the pair ABI. This preserves a word inside an opaque variant result.

The regression covers nine cases: a nested tiny method, a direct function with one call site, and a forced inline method. Other cases cover an opaque getter, a wide cast, a `u64` getter, and a `u64` field. They also cover a separate opaque consumer and a wrapped getter. Each case runs on x86-64, ARM64 and Wasm, in plain and sanitized modes.

Local validation uses the compiler inputs from PR #13897 at `8fbfcf413dde4c6c13e4b253d6c47987db21fc1f`. Head `280b5f8054d5e808f08b90fdf6047175d2d096db` changes only a constructor test. The compiler and standard library inputs are identical.

- Ten focused Go tests produce 144 pass results and zero skips on both stage 2 and stable stage 3.
- The other 23 semantic inliner tests produce 61 pass results and zero skips on stable stage 3.
- Stages 3 and 4 produce identical binaries at SHA-256 `a1eb68033f256b7ad493feb154ee7dbd78c23f7a5a6156678caab7dc6d0d311f`.
- The current owner compiler returns 2 on the direct and wrapped controls on all three targets. The three `@noinline` controls return 0. The candidate returns 0 on all six checked cases, with zero live bytes.
- The new IR control keeps the wrapper out of line for a bodyless getter. A known getter with an independent result still permits the splice.
- The updated constructor union test produces five pass results and zero skips on the candidate.
- Four other groups that failed on main `7cf07064` produce 19 pass results and zero skips. These cover array methods, display method retention, ndarray reuse decisions, and the string contract program.
- The formatter reproduces `compiler/seminline.fern` byte for byte.

Local Go overlays select verified compiler binaries. The extra inliner tests also select the current compiler sources and standard library. The primary program tests use the frozen standard library from main `33b2166`. Program execution, allocation limits and assertions remain unchanged.

The cost comparison uses two release stage-2 compilers from one stage-1 compiler. Both compile the same fixed `checker.fern` subject with memoization disabled. Each compiler has an independent, warmed cache.

The instruction count falls from 39,967,273,663 to 31,401,151,381, a reduction of 21.43%. The unchanged baseline profile serves the final correction as well. The candidate profile is a new run. The emitted assembly text changes from 31,312,450 to 31,531,291 bytes. The changed assembly requires the runtime and bootstrap gates above. Raw profiles, exact outputs, compiler images, programs, and logs remain in the local evidence storage.

This slice does not complete #4412. The validation does not include a full package sweep, native macOS execution, or candidate CI. The profile has no symbols, so it does not attribute allocation counts. It does not establish native latency or performance across the benchmark corpus.
