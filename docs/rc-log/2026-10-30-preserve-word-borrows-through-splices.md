# Preserve resource-word borrows through semantic splices

This record describes commit 7c32f44. A later parent-owner control failed on that head. See [the follow-up record](2026-10-30-retain-transitive-word-owners.md) for the repair, current measurements, and remaining serve-allocation hold.

A buffer handle can depend on the lifetime of its record. A call keeps its record argument alive until the call returns. A semantic splice can make the field read the last use of that record. Its finalizer then runs before a later call uses the handle.

Check this dependency at `splice_calls`, before both splice paths. Keep the late `grown_tiny` check. Functions with one call site and forced `@inline` functions obey the same lifetime condition.

A scalar field read starts a resource-word dependency when its owner has a custom finalizer. Ordinary numeric array elements do not start this dependency. Explicit raw word reads remain conservative. Follow the dependency through casts, copies, phi nodes, and pair payloads. Compute return summaries over known callees to a fixed point. Unknown callees and declarations without bodies remain conservative for reference arguments.

Byte-scan kernels return copied counts, indices, or hashes. These results do not borrow the storage of their arguments. Keep these known results distinct from opaque getter results. In particular, `__count_below_u64` returns a count. Its result must not block ordered-map splices that permit buffer reuse.

The regression covers nine lifetime cases on x86-64, ARM64, and Wasm. Each case runs in plain and sanitized modes. An IR control keeps a bodyless getter out of line. A known independent getter and a copied kernel result permit the splice. The copied-index control fails with exit 79 on the rejected broad prototype and passes with this repair.

Local validation uses the compiler inputs from PR #13897 at `8fbfcf413dde4c6c13e4b253d6c47987db21fc1f`. The compiler and standard library remain identical at `daeec46f78fcdda81d74a98294d7a33a1357d637`. The later changes affect a constructor test and the playground editor.

- Ten focused Go tests produce 144 pass results and zero skips on both stage 2 and stable stage 3.
- The stable compiler also passes all 144 results with the current standard library.
- The other 23 semantic inliner tests produce 61 pass results and zero skips on each compiler.
- The constructor union test produces five pass results and zero skips on the stable compiler.
- Four groups that failed on main produce 19 pass results and zero skips on the stable compiler.
- Stages 3 and 4 have identical SHA-256 `b9fbf56e1c65f51c80e52471f15e474129d747656dbf2b21a98b33d20eb72feb`.
- The formatter reproduces `compiler/seminline.fern` byte for byte.

The reuse controls run on all three targets with both compiler generations. The unique ordered map allocates 468 times at both two and eight update rounds. The shared map allocates 566 and 860 times. Each map run reports equal allocation and free counts with zero live bytes.

The FIP array map preserves its donor and does not increase the heap bump count. These are the unchanged program assertions. No allocation limit or baseline changes.

Local Go overlays select the verified compiler binaries through read-only path adapters. The first primary tests use the frozen standard library from main `33b2166`. The extra inliner tests and the additional stable primary tests select the current compiler sources and standard library. Program execution and assertions remain unchanged.

The cost comparison uses two release stage-2 compilers from one stage-1 compiler. Both compile the same fixed `checker.fern` subject with memoization disabled. Each compiler has an independent, warmed cache. The unchanged baseline profile is reused. The candidate profile is a new run.

The instruction count changes from 39,967,273,663 to 34,758,103,997 (-13.03%). The emitted assembly changes from 31,312,450 to 31,446,280 bytes. This change affects generated code, so runtime tests are the primary gate. Bootstrap identity is a separate gate. Raw profiles, exact outputs, compiler images, programs, and logs remain in the local evidence storage.

This slice does not complete #4412. Local validation does not include a full package sweep or native macOS execution. Candidate CI remains a separate gate. The profile has no symbols, so it does not attribute allocation counts. It does not establish native latency or performance across the benchmark corpus.
