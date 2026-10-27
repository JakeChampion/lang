# Two-field records in pair returns

The fixed checker compile records 18,489,777 box allocations with the prior compiler after self-reproduction.
`semsource.define` accounts for 233,920 calls to `__fern_arr_box` across 116,960 calls.
It returns a `Value` record with a `State` field and an integer field.
The current pair-return pass accepts the equivalent tuple, but it excludes that record.

The change extends the current pair representation to two-field records that satisfy its current part checks.
The first field uses the primary result, and the second field uses the current pair word.
Records with views or finalizers retain their boxes.
Actual function references and callers that retain a whole result also retain their current ABI.
The pass reuses the current extraction and reconstruction paths.

The physical contract table now describes the reference units in record pair results.
A record with a wide first field and a reference second field retains its complete pair contract.
The scalar-lane contract rewrite applies only to eligible scalar records.
The initial private prototype omitted these contract updates, and the certifier refused its first reference result.
The refusal log and source snapshot remain in the workspace.
The evidence includes the refusal and graph dumps.

## Correctness

The new record fixture covers direct calls, forwarded calls, joins, tail recursion and a borrowed record result.
It also covers a reference in either field, two references, a wide first field and retained source aliases.
The function-pointer control and whole-record control each allocate 50 boxes.
Before the change, the fixture records 425 allocations.
After the change, it records 100 allocations.

The guard fixture retains a finalizer and a view holder.
Both versions record 100 allocations and execute 50 finalizers.
The heap-alias fixture uses a heap string in the second result position.
It also returns the same heap array in both result positions.
Its allocation count decreases from 100 to zero.

The three fixtures give 54 runs across before and after compilers, three targets and three modes.
The modes are default, optimized and sanitizer-enabled.
Every run reports zero live bytes.
The self-compiled candidate repeats all 27 after cases with the same results.

The pair-return, scalar-record return, instrumented return and physical-contract suites pass in 146.020 seconds.
These initial primary checks completed before the compiler self-reproduction check.
The supplementary heap-alias repository cases pass on all three targets in 2.440 seconds.
Source checks, source lint, the feature and complexity ratchets, vet and test-name checks also pass.
The compiler produces byte-identical stage-3 and stage-4 binaries.
Their SHA-256 is `64f876ff80e4db91d1eb614605a7fe567bbad13ecb061dce204defb5c0b975a0`.

## Measurement protocol

The frozen subject is the complete `compiler/checker.fern` at `23c5bc2e4a5993ffe092d04b5ad1dcf771af0269`.
The standard library comes from that same snapshot.
The target is x86-64 Linux, with `-O -emit asm` and a private, warm certifier memo.
Each profile covers a complete compile with a successful exit.
Raw profiles and logs retain their original data.

The primary comparison uses two stage-2 compilers from one producer.
That producer has SHA-256 `bddb8e23b04acf6db660c5217d7472405c1898b80b20853fbf12c7e5762c19ff`.
It implements the prior phi-label fix and does not implement record pair returns.
The primary comparison prices the new analysis and the changed work to emit the fixed subject.
It excludes the new ABI effect on the compiler's own machine code.

| Primary metric | Baseline stage-2 | Candidate stage-2 |
|---|---:|---:|
| Instructions | 25,074,649,447 | 25,077,429,011 |
| Calls to `__fern_arr_box` | 18,489,777 | 18,495,825 |
| Subject assembly bytes | 26,675,438 | 26,653,064 |

The primary comparison adds 2,779,564 instructions, or 0.011085%, and 6,048 box calls.
It therefore does not show a stage-2 compiler speed gain.
The fixture allocation decrease describes generated-program behavior.

The later candidate stage-3 compiler uses candidate stage-2 as its producer.
Its profile is a separate bootstrap observation.
It does not share the producer of the baseline, so it is not the primary A/B result.

| Bootstrap observation | Prior compiler | New self-compiled compiler |
|---|---:|---:|
| Instructions | 25,074,649,447 | 25,031,956,216 |
| Calls to `__fern_arr_box` | 18,489,777 | 17,926,218 |

This observation records 563,559 fewer boxes and 0.170265% fewer instructions.
The largest box decreases occur in `ssaunits.block_claims`, `semsource.build` and `asmcore.lab_new`.
The `semsource.define` site retains its 233,920 box calls.
Candidate stage-2 and stage-3 compilers emit byte-identical checker output with SHA-256 `6ede521a93108492362e54f018dc863eef2734d8457f13a553a89f3b238cd8e8`.
No native latency comparison supports this slice.
This slice does not complete issue #4412.

## Evidence

The source patch applies to `1506551e149750e34dc5239368683577df0deda9`.
`source-hashes.json` identifies every frozen compiler and standard-library source.
`compiler-hashes.json` identifies the compiler artifacts.
The JSON records contain commands, compiler hashes, fixture hashes, statuses and raw output.
The Python coordinators retain their original workspace paths for provenance.

Gzip archives preserve raw Callgrind files and symbol maps without data loss.
The archive records include raw and compressed hashes.
The release and debug load proofs precede address attribution.
The box count sums caller-edge counts for the nonrecursive `__fern_arr_box` function.

## Repeat the primary comparison

The script requires the named producer, Valgrind 3.24.0, Git and the pinned repository toolchain.
A bootstrap of the frozen baseline source supplies that producer.
Run these commands from the publication checkout:

```sh
bash docs/benchmarks/record-pair-returns-2026-10-27/reproduce.sh /absolute/path/to/prior-producer /tmp/record-pair-returns
go test ./internal/testing/e2ecompiler -run '^TestSelfHost(PairReturn|ScalarRecordReturns|ScalarRecordReturnsInstrumented|SelectedPhysicalContracts)$' -count=1 -v
```

The script checks the producer hash and reconstructs both frozen sources in separate worktrees.
It builds both stage-2 compilers with that producer and records their complete profiles.
The generated output can differ because this change affects the private ABI.
The e2e suites and compiler self-reproduction are the correctness gates.
