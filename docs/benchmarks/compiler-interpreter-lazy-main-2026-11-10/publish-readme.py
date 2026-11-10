from pathlib import Path
import json

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
dst = repo / 'docs/benchmarks/compiler-interpreter-lazy-main-2026-11-10'
summary = json.loads((dst / 'summary.json').read_text())
prep = json.loads((dst / 'prepare.json').read_text())
validation = json.loads((dst / 'validation/validation.json').read_text())
tls = json.loads((dst / 'integration/tls-control-result.json').read_text())
tests = next(r for r in validation['runs'] if r['label'] == 'tests')
def f(number):
    return f'{number:,.9f}' if isinstance(number, float) else f'{number:,}'
def table(groups):
    rows = ['| Measurement | Main baseline | Lazy candidate |', '| --- | ---: | ---: |']
    for name, title in groups:
        group = summary[name]
        for key, label, suffix in [('wall_median_s', 'median wall time', ' s'), ('cpu_median_s', 'median CPU time', ' s'), ('peak_rss_median_kib', 'median peak RSS', ' KiB')]:
            rows.append(f'| {title} {label} | {f(group["baseline"][key])}{suffix} | {f(group["candidate"][key])}{suffix} |')
        ranges = ['–'.join(f(x) for x in group[n]['wall_range_s']) for n in ('baseline', 'candidate')]
        rows.append(f'| {title} wall range | {ranges[0]} s | {ranges[1]} s |')
    return '\n'.join(rows)
counts = []
for group, title in [('first', 'First group'), ('second', 'Second group')]:
    values = summary[group]['allocations']
    counts.append(f'| {title} | {values["baseline"]["allocs"]:,} | {values["candidate"]["allocs"]:,} | {values["baseline"]["allocs"] - values["candidate"]["allocs"]:,} |')
ir = {r['compiler']: r['guest_instructions'] for r in summary['instructions']['runs']}
work = {r['compiler']: r['guest_instructions'] for r in summary['compiler_work']['runs']}
full = summary['full']
text = f'''# Lazy argument-target lookup after current main

Status: [record] matched comparison from 2026-11-10.

The baseline is main `{prep['baseline_source']}`.
The candidate is `{prep['candidate_source']}`.
Main contains the Paper Back, resumed TLS expectation, nested-owned-field checker, and CI worker repairs.
This comparison does not reuse the older main totals.
The [compressed patch](implementation.patch.gz) retains the exact product change and regression controls.

Both compilers retain the upstream `Funcs` Map index and its 4,000-function cost guard.
This change only defers unused argument-target lookup.
Free calls with implicit type arguments retain eager lookup.
Other calls resolve once before the first direct call argument needs the result.
The flag remembers both success and a miss.
Closures and both addressability queries retain their prior behavior.

The interpreter source matches the earlier qualified lazy implementation byte for byte.
The [earlier semantic audit](original-lazy-semantic-audit.json) describes its immutable lookup inputs.
The [source audit after collection](post-run-source-check.json) verifies this identity and the frozen worktrees.
The complete compiler and standard library changed, so this archive retains a separate matched comparison.

## Inputs and builds

The fixed input and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
Both compilers use qualified producer `{prep['producer_source']}`.
Its SHA-256 is `{prep['producer_sha256']}`.
The [preparation record](prepare.json) retains four successful fresh builds and all executable identities.

Normal executables omit debug variables and embedded source files.
Profiles and allocation counters are off, with ownership certification on.
Separate counter executables enable allocation counts.
The proof cache is off for interpretation and cold compilation.
Each compiler uses its own primed cache for warm compilation.

## Correctness and integration

Pinned source check, read-only format check, Go vet, and CI self-tests exit zero.
Twelve targeted suites report {tests['passes']:,} passing test and subtest results, with zero skips.
The package completes in 266.241 seconds.
The [validation records](validation/validation.json) retain the exact selectors and commands.

The suites cover x86-64, ARM64 under QEMU, WebAssembly, and the function-count guard.
They retain the nested-owned-field positive and negative controls.
The same-count REPL regression preserves replacement, declaration order, and earlier session snapshots.

Both TLS selectors that failed the superseded head also pass after the main integration.
Their [separate control](integration/tls-control-result.json) reports {tls['passes']} test and subtest passes and zero skips.
These tests start after the original performance sequence finishes.
Main supplies the expectation repair for the retained client certificate.
The archive retains the original failed CI logs and the baseline conflict resolution.
Every numerical baseline row and tolerance matches the newer main at integration.

## Normal interpreter samples

Five pairs alternate compiler order on the first 32-record group.
Three pairs alternate order on the second 32-record group, with 127,126 decoded bytes.
All sixteen normal commands exit zero with identical TAP output within each group.
CPU time is each child's user time plus system time.
The [raw records](subsets/results.json) retain every sample and outlier.

{table([('first', 'First group'), ('second', 'Second group')])}

Paired CPU changes for the first group are {', '.join(f'{x:+.4f}%' for x in summary['first']['paired_cpu_change_percent'])}.
Paired CPU changes for the second group are {', '.join(f'{x:+.4f}%' for x in summary['second']['paired_cpu_change_percent'])}.
Negative values indicate less CPU time for the candidate.
Both groups contain slower candidate samples.
These shared-host timings establish no consistent speedup.
The report retains all memory measurements without a heap or residency attribution.

## Allocation events and guest instructions

| Allocation events | Main baseline | Lazy candidate | Fewer events |
| --- | ---: | ---: | ---: |
{chr(10).join(counts)}

Every counter run frees all allocations and reports zero live bytes.
Each pair produces identical TAP output.
Counter executables are separate from production timing executables.

One Valgrind 3.24.0 sample per normal executable measures the first group.
Guest instructions are {ir['baseline']:,} for main and {ir['candidate']:,} for the candidate.
The difference is {ir['baseline'] - ir['candidate']:,}, or {(1 - ir['candidate'] / ir['baseline']) * 100:.4f}% fewer instructions.
The [instruction record](instructions/results.json) retains commands, raw counts, output hashes, and tool identities.

Cache simulation, branch simulation, and child tracing are off.
Both runs exit zero with identical TAP output.
No timing claim uses instrumented runs.

## Complete compilation and full corpus

Both compilers build the complete fixed Opus reference program and its imports to native code.
Three pairs alternate compiler order per cache mode.
Separate probes verify {summary['cache_probes'][0]['cache_hits']} hits and zero misses for each compiler's own cache.
Actual measurements use normal verifier defaults without the report flag.

{table([('cold', 'Cold'), ('warm', 'Warm')])}

All sixteen compilation commands exit zero and produce identical native output.
The [compilation records](compilation/results.json) retain every sample and cache probe.
Both cold and warm candidate medians are slower in this comparison.
Paired cold CPU changes are {', '.join(f'{x:+.4f}%' for x in summary['cold']['paired_cpu_change_percent'])}.
Paired warm CPU changes are {', '.join(f'{x:+.4f}%' for x in summary['warm']['paired_cpu_change_percent'])}.

All three cold pairs and two warm pairs use more candidate CPU time.
These controls establish no general compiler performance gain.

All three slower cold CPU pairs prompted a separate check of complete-compilation work.
One normal-binary instruction sample per compiler records {work['baseline']:,} guest instructions for main and {work['candidate']:,} for the candidate.
The [separate instruction record](compilation-instructions-v2/results.json) retains both raw counts and identical native output hashes.
Its candidate-minus-main difference is {work['candidate'] - work['baseline']:+,} instructions.
This diagnostic pair follows every normal timing and TLS control.
It does not establish a timing cause or a general compiler performance result.

A tool-version preflight detected the missing path for the private Valgrind libraries before either compiler measurement started.
The [preflight record](compiler-work-preflight.json) retains that failure and the terminated waiter with zero measurement samples.
The corrected controller supplies the private library path and uses a separate results directory.

The normal candidate completes all 1,896 records in {full['wall_ns'] / 1e9:.9f} seconds with exit zero.
User CPU time is {full['cpu_user']:.6f} seconds, and system CPU time is {full['cpu_system']:.6f} seconds.
Peak RSS is {full['peak_rss_kib']:,} KiB.
Its full TAP hash is `{full['stdout_sha256']}`.
This output matches the compiled program output.

The [full control](full/results.json) has no paired baseline timing.
The normal full-corpus run does not measure allocation balance.

The diagnostic limit remains 1,980 seconds.
The CI package budget remains 33 minutes.
The corpus, exclusions, proof rules, negative controls, and tolerances stay unchanged.
Complete exact-head CI remains the merge gate.

## Reproduction and limits

Use isolated worktrees at the recorded revisions and the same qualified producer.
The [preparation script](prepare.py), [measurement script](measure.py), and [compilation script](compile-controls.py) retain every command.
Their absolute paths identify the original worktrees.
The [controller](collect.py) waits for builds and targeted validation before subset timings.

Compilation follows subset controls, then full interpretation, then instruction counts.
The TLS control follows that original sequence.
The additional complete-compilation instruction control follows the TLS control.
The shared host limits timing precision.

Executables and proof caches stay outside this archive.
The ignored originals remain under `build/interpreter-lazy-main-qualification-20261110-0710` in the primary checkout.
The [manifest](manifest.json) hashes the retained data files.
The [compression identities](compression-identities.json) preserve original raw bytes with lossless gzip.
The README and manifest itself stay outside the data hash list.

Verify local files with `python3 verify.py`.
Verify committed blobs with `python3 verify.py --commit HEAD`.
Do not pool results from different compiler sources.
These measurements establish no external rank or full-corpus speedup factor.
'''
(dst / 'README.md').write_text(text)
