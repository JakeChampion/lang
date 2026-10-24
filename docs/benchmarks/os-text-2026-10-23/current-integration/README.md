# Current OS text checks

The source commit is `81fbf50964f8763695b04b8ceeff80dbf56517bc`.
The dependency base is `f163d78dc4d931b92bfa6a127a93b25f3ddba959`.
Earlier evidence in the parent directory retains its original source scope.

Directory APIs reject a malformed name with `IoError.InvalidUtf8`.
The complete result fails. Callers receive no partial list or replacement name.
The same rule applies to link targets and current-directory paths.
The OS text audit records the contract for each other text producer.

The family suite runs 41 cases with no failures.
Two Darwin cases skip on this Linux host.
The other 39 cases cover directory names, repeated handle reads, link targets and invalid current-directory paths.
They also cover Reader text and extended-attribute bytes.
The Go interpreter and checker suite runs 2205 cases with no failures.
Three synthetic-AST cases skip.

The final compiler matrix runs 20 commands against two exact inputs.
The inputs contain 88 native checks and 72 portable checks.
The host creates 13 malformed UTF-8 filename classes and valid multibyte controls.
Both checkers and both interpreters pass.
All applicable builds pass, with Darwin checked by compilation.
All five x86-64, ARM64 and WASI executions balance allocations with zero live bytes.

The public main compiler and Go interpreter fail the first malformed-name check on the same native input.
The public compiler's x86-64 output also fails that check.
The candidate passes the same input on both interpreters and both native execution targets.
The baseline report records the expected failure value, 6.

The published stage0 pin produces byte-identical stages 2 and 3 at 14428560 bytes.
Their SHA-256 is `76533dbff0e1a8926160e10d32c17232d32b2a32241f3049968beee4e11f2345`.
All 11 strict driver size gates pass without baseline changes.
The feature census and complexity limits pass without changes.
All 1201 canonical Fern format checks and the Go format gate pass.
Go vet passes for the changed Go packages and both test packages.

The manifest records 497 source file hashes and 27 evidence artifacts.
The source patch passes a reverse-application check.
The archive contains all 48 output logs and both exact Fern input files.
The matrix generator records each malformed byte sequence.
Fern input strings contain valid UTF-8.
