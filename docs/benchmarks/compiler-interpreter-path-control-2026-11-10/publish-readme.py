from pathlib import Path
import json, statistics

repo = Path('/workspace/lang-interpreter-dispatch-20261109')
dst = repo / 'docs/benchmarks/compiler-interpreter-path-control-2026-11-10'
d = json.loads((dst / 'results.json').read_text())
v = json.loads((dst / 'retained-main/validation.json').read_text())
full = json.loads((dst / 'retained-main/build-full.json').read_text())
tests = next(r for r in v['runs'] if r['label'] == 'tests')
tables = []
for mode, title in [('cold', 'Cold'), ('warm', 'Warm')]:
    s = d['summary'][mode]
    tables += [f'| {title} median wall time | {s["baseline"]["wall_median_s"]:.9f} s | {s["candidate"]["wall_median_s"]:.9f} s |',
               f'| {title} median CPU time | {s["baseline"]["cpu_median_s"]:.9f} s | {s["candidate"]["cpu_median_s"]:.9f} s |',
               f'| {title} median peak RSS | {s["baseline"]["peak_rss_median_kib"]:,.0f} KiB | {s["candidate"]["peak_rss_median_kib"]:,.0f} KiB |',
               f'| {title} wall range | {s["baseline"]["wall_range_s"][0]:.9f}–{s["baseline"]["wall_range_s"][1]:.9f} s | {s["candidate"]["wall_range_s"][0]:.9f}–{s["candidate"]["wall_range_s"][1]:.9f} s |']
text = f'''# Rejected lazy lookup after the fixed compiler timing control

Status: [record] rejected experiment from 2026-11-10.

The lazy argument-target shortcut is rejected.
It reduces allocation events and guest instructions on interpreter subsets.
The complete-compilation timing studies retain higher candidate medians with substantial paired variation.
Those work-count benefits do not justify the unresolved compilation cost.
The timing cause remains unknown.

The final product retains eager lookup and the complete upstream `Funcs` Map index.
The two regression files still cover generic calls and same-count REPL replacement.
The [decision record](decision.json) and [removed patch](removed-trial.patch.gz) preserve the rejected code and its rationale.
Earlier measured archives remain unchanged with their exact source identities.

## Fixed design

The baseline compiler source is `{d['baseline_source']}`.
The lazy candidate source is `{d['candidate_source']}`.
Both executables use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The fixed Opus reference program and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
The [earlier comparison](../compiler-interpreter-lazy-main-2026-11-10/README.md) retains the original timing loss and near-equal complete-compilation instruction totals.

This control uses copies of those exact executables at equal-length `base/fern` and `cand/fern` paths.
The collector verifies their SHA-256 before every run.
It fixes eight pairs per proof-cache mode before collection starts.
Four pairs use each compiler order.
No result changes the sample count or stopping rule.

Each executable primes its own proof cache and runs a separate probe.
Both probes report 112 hits and zero misses.
Actual measurements use normal verifier defaults without the report flag.
Cold compilation disables the proof cache.
This is not a cold operating-system page-cache study.

Normal executables omit debug variables, embedded source, profiles, and allocation counters.
Ownership certification remains on.
Per-child `wait4` reports CPU time and peak RSS.
The host remains shared, without exclusive CPU or frequency control.
Small read-only CI observations can overlap.

## Complete-compilation results

All 36 commands exit zero.
These commands comprise 32 timed samples, two cache primes, and two probes.
Every command emits identical native output with SHA-256 `e8ddd643bdc9d72008d930fc42ee8f1574d6f39ae3628891ea0f828f3c67b46f`.
The [raw records](results.json) retain every sample, command, resource measurement, and identity.

| Measurement | Main baseline | Lazy candidate |
| --- | ---: | ---: |
{chr(10).join(tables)}

Paired cold CPU changes are {', '.join(f'{x:+.4f}%' for x in d['summary']['cold']['paired_cpu_change_percent'])}.
Paired warm CPU changes are {', '.join(f'{x:+.4f}%' for x in d['summary']['warm']['paired_cpu_change_percent'])}.
Negative values indicate less candidate CPU time.
Five cold pairs and four warm pairs use more candidate CPU time.
The paired changes have large variation.
Both candidate medians remain higher.

The results do not establish a specific code-layout or residency cause.
They do not establish a general compiler speedup.
No earlier timing or work-count sample is removed.
This study remains separate from the earlier comparison.

## Retained source and correctness

Retained source `{full['source']}` removes the lazy shortcut.
Its compiler and standard library exactly match main `c9547605`.
The [source audit](post-run-source-check.json) verifies that equality.
The fresh production rebuild is byte-identical to the measured main baseline.
Its SHA-256 is `{full['runs'][0]['binary_sha256']}`.

Pinned source check, read-only format check, and Go vet exit zero.
The [targeted validation](retained-main/validation.json) reports {tests['passes']} passing test and subtest results and zero skips.
The suites cover x86-64, ARM64 under QEMU, WebAssembly, the function-count guard, and nested-owned-field negative controls.
The generic-call and same-count session regressions remain active.

The rebuilt normal compiler completes all 1,896 reference records with exit zero.
Its full TAP hash is `{full['runs'][1]['stdout_sha256']}`.
The [full correctness control](retained-main/build-full.json) overlaps targeted validation.
Its duration does not represent a performance comparison.
Normal full-corpus execution does not measure allocation balance.

The diagnostic limit remains 1,980 seconds, and the CI package budget remains 33 minutes.
Corpus, exclusions, proof rules, negative controls, benchmarks, and tolerances stay unchanged.
Complete exact-head CI remains the merge gate.

## Reproduction and records

The [collector](collect.py) retains the exact commands, order, cache setup, and fixed sample count.
Its paths identify the original frozen worktrees and copied executables.
The [validation script](retained-main/validate.py) and [rebuild control](retained-main/build-and-full.py) retain the final source checks.
Executables and proof caches remain outside this archive.

The [manifest](manifest.json) hashes the retained data files.
The README and manifest itself are outside that data hash list.
The [compression identities](compression-identities.json) preserve original raw bytes with lossless gzip.
Verify local files with `python3 verify.py`.
Verify committed blobs with `python3 verify.py --commit HEAD`.

The ignored originals remain under `build/interpreter-compiler-path-control-20261110-0812` and `build/interpreter-retained-main-validation-20261110-0820`.
These records establish no external rank.
The next optimization needs a separate matched comparison from actual main.
'''
(dst / 'README.md').write_text(text)
