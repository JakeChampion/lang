# JSON pair construction and collection contracts

Source: `fce3277515f49791d3f136fd123a3a1253058f49`.
The compiler and library snapshot is `9a6cb8d1a5e8b6d10e57147c07909ef8d6bd4324`.
The later commit changes one collection test to use `json.from`.
The compiler, library and Go oracle sources remain identical.

`json.from(pairs)` constructs a `JsonObject` from `(string, JsonValue)` pairs.
The first occurrence of a key sets its position.
The last occurrence sets its value.
The constructor shares the index policy of `object_of`.
Tests cover empty input, mixed variants, duplicate keys, indexed objects and an entries round trip.
The collection vocabulary suite also uses the new pair constructor.

The existing collection implementation supplies the shared vocabulary from #12711.
Its tests cover Map, OrdMap, PMap, JsonObject, Set, OrdSet and PSet.
The map tests check membership, removal, update on absence and merge precedence.
The set tests check the shared set operations.
The JSON constructor supplies the remaining pair-construction API.

The bootstrap lock comes from the published release
[`stage0-20261024-5e63712`](https://github.com/JakeChampion/lang/releases/tag/stage0-20261024-5e63712).
The release workflow validated all three host binaries.
The evidence includes the exact lock and the downloaded producer hash.
The old published pin failed the imported tuple-array signature.
The earlier evidence directory retains that failure.

The final matrix has 40 commands for two programs.
Both Go checks and interpreter runs pass.
The current compiler and the actual released pin each check and interpret both programs.
Each producer compiles both programs for four targets.
Native x86, ARM64 under qemu and WASI executions produce identical TAP output.
Darwin is a compile check only.

The two suites have 14 TAP cases.
All 12 allocation checks balance with zero live bytes.
The collection suite uses the final test revision.
Its 20 commands repeat after the constructor change.
The unchanged JSON suite retains its 20 passing commands from the compiler and library snapshot.
Each matrix row records its source revision.

Two Go TAP runner tests pass before the final test-only change.
The final Go interpreter matrix also passes the updated collection suite.
All three changed Fern source files pass the format check.

The released pin builds all three bootstrap stages with the same SHA-256:
`75678f8c6ae21e5e6e17c85ad621f0444fc3c4a6d955e991733c4e57b1694204`.
This is also the current public compiler image.
The public branch separately passes all 11 strict driver size gates.

The manifest records 495 compiler and library source files and 16 evidence artifacts.
The archive contains all 80 matrix output and error logs.
The complete patch applies in reverse to the source revision.
The final oracle, bootstrap stages and published pin remain in the workspace.
