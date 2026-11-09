from pathlib import Path
import json,hashlib,shutil,subprocess
repo=Path('/workspace/lang-performance-20261109-1950')
root=repo/'build/postmerge-qualification-20261109-1950'
work=json.loads((root/'measurements.json').read_text())
validation=json.loads((root/'validation-result.json').read_text())
assert work['complete'] and validation['go_exit_code']==0 and validation['selftest_exit_code']==0
assert subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True)==''
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()==work['candidate_source']
dest=repo/'docs/benchmarks/compiler-semantic-liveness-postmerge-2026-11-09'
assert not dest.exists()
dest.mkdir()
for p in sorted(root.iterdir()):
    if p.is_file() and p.suffix in ['.py','.json','.log','.stdout']:
        shutil.copyfile(p,dest/p.name)
patch=subprocess.check_output(['git','diff',work['baseline_source'],work['candidate_source'],'--','compiler/ssadeps.fern','compiler/ssalive.fern','internal/testing/e2ecompiler/self_host_ssa_dependency_verify_test.go'],cwd=repo)
(dest/'reuse.patch').write_bytes(patch)
readme=f'''# Semantic map reuse after later main changes

Status: [record] cold allocation and output controls from 2026-11-09.

The baseline compiler source is `{work['baseline_source']}`.
The candidate source is `{work['candidate_source']}`.
The fixed checker input source is `{work['fixed_input_source']}`.
The pinned producer source is `{work['producer_source']}`.
The [manifest](manifest.json) records exact binary and data hashes.

## Allocation and output controls

| Complete cold compilation | Baseline allocations | Candidate allocations |
| --- | ---: | ---: |
| Emit assembly | 52,703,024 | 52,685,747 |
| Emit native executable | 51,660,258 | 51,642,981 |

Each comparison uses 17,277 fewer allocation events.
All four runs free every allocation and report zero live bytes.
The [raw commands and results](measurements.json) retain each invocation.
The [native allocation check](native-allocation-check.json) records the separate native counts.
Assembly and native output hashes match within each comparison.

Both compiler builds enable `FERN_LEAKCHECK=1` and disable CPU and allocation profiles.
The builds omit `-g` and retain ownership certification.
The measured cold runs use `FERN_RC_CERTIFY_MEMO=0` and normal verifier defaults.
Counter-enabled binaries change allocator fast paths.
These controls establish allocation-event reductions and output agreement.
They establish no runtime allocator cost, instruction reduction, or timing gain for these source identities.

The [earlier complete comparison](../compiler-semantic-liveness-main-2026-11-09/README.md) uses its own exact source pair.
Its instruction and timing samples do not measure these later main changes.

## Correctness and environment

The targeted dependency and liveness tests pass in {validation['go_package_seconds']:.3f} seconds with explicit Go exit zero.
The log contains {validation['passing_results']} passing test and subtest results and no skips.
The tests cover x86-64, ARM64 under QEMU, and WebAssembly.
Legacy and reused flows, invalid metadata, and verifier-off controls remain active.
The source check, Go vet, and CI self-tests also pass.

The executor changed before the first attempt retained a result.
This comparison uses new checkouts, binaries, and output paths.
No ignored artifact from the previous executor was recovered.
The [toolchain record](toolchain.json) identifies this fresh environment.
The native test wrapper retains its core-dump controls without an overlay.
These local checks do not replace the exact-head full CI verdict.

## Reproduction

Create clean checkouts of the recorded candidate, baseline, and fixed input revisions.
Use the producer identified by source and hash in the manifest.
Set local paths in [measure.py](measure.py) for those checkouts and a new output directory.
Run the script with Python 3.
Run the commands from [validation-result.json](validation-result.json) with the recorded tools.
Retain all raw logs and allocation counts.
'''
(dest/'README.md').write_text(readme)
manifest={k:work[k] for k in ['baseline_source','candidate_source','fixed_input_source','producer_source','producer_sha256','compiler_binaries','target','debug','certification']}
manifest['claim']='Cold allocation and output controls only. No new instruction or timing comparison.'
manifest['files_sha256']={str(p.relative_to(dest)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(dest.rglob('*')) if p.is_file()}
(dest/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
old=repo/'docs/benchmarks/compiler-semantic-liveness-ci-2026-11-09'
repro=repo/'build/reproduce-ci-asm-20261109-1950'
e=json.loads((repro/'reproduction.json').read_text())
assert e['complete']
for name in ['base-count-byte.s','candidate-count-byte.s']:
    assert not (old/name).exists()
    assert hashlib.sha256((repro/name).read_bytes()).hexdigest()=='aee54ecd1cad235488954dcd50ea22e0848e8838578560c7b4441aafe718d55f'
    shutil.copyfile(repro/name,old/name)
(old/'reproduction').mkdir()
for p in sorted(repro.iterdir()):
    if p.is_file() and p.suffix in ['.json','.log','.stdout']:
        shutil.copyfile(p,old/'reproduction'/p.name)
p=old/'README.md'
p.write_text(p.read_text()+'''\n## Assembly archive repair

The previous commit omitted two assembly files that the manifest lists.
On 2026-11-09, we reproduced these files from the exact source revisions and the same producer.
Both files match the original assembly hash.
The rebuilt compiler executables also match the original recorded hashes.
The [reproduction record](reproduction/reproduction.json) retains the new commands and identities.
No ignored artifact from the previous executor was recovered.
''')
p=old/'manifest.json';d=json.loads(p.read_text())
d['files_sha256']={str(p.relative_to(old)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(old.rglob('*')) if p.is_file() and p.name!='manifest.json'}
p.write_text(json.dumps(d,indent=2)+'\n')
print('Created post-merge allocation archive and repaired omitted assembly evidence.')
