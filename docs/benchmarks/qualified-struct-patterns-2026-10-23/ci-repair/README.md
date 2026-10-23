# CI fixture repairs

The CI run for `0574d9dfca7998e4aea5df5b6c4dd39af3ab7bef` found two stale fixtures after the merge of main `8207edece`.

The lexer now imports `std/unicode` and `std/utf8`. The ARM64 fixed-point test omitted the standard library root for its lexer case.
Both generations now receive that root, as the other imported cases do.
The full `TestSelfHostStage2FixpointArm64` test passes in 94.693 seconds.
This test builds an ARM64 compiler, executes it under QEMU, and compares its assembly with the x86-64 host compiler's assembly.

The custom WIT world now declares both stdout and stderr.
It therefore has five interfaces, including the error, poll, and stream dependencies.
The test now checks all five interfaces against one expected list.
`TestComposeFromUserWorld` passes in 22.600 seconds. The test validates the component and executes it with Wasmtime.

`go vet ./...` passes. These changes affect the test fixtures only.
The earlier compiler bootstrap evidence retains its original source scope.
