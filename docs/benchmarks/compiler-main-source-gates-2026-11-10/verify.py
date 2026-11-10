from pathlib import Path,PurePosixPath
import argparse,gzip,hashlib,io,json,subprocess,tarfile
HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[2]
p=argparse.ArgumentParser();p.add_argument('--commit');args=p.parse_args()
def read(name):
 path=HERE/name
 return subprocess.check_output(['git','show',args.commit+':'+str(path.relative_to(ROOT))],cwd=ROOT)if args.commit else path.read_bytes()
sha=lambda b:hashlib.sha256(b).hexdigest()
m=json.loads(read('manifest.json'))
for name,digest in m['sha256'].items():assert sha(read(name))==digest,name
for name,digest in json.loads(read('raw-identities.json')).items():assert sha(gzip.decompress(read(name)))==digest,name
before=gzip.decompress(read('reference-before.json.gz'));after=gzip.decompress(read('reference-current.json.gz'))
b=json.loads(before);a=json.loads(after)
assert {k:v for k,v in b.items()if k!='legacy_sources'}=={k:v for k,v in a.items()if k!='legacy_sources'}
changes=[name for name,h in b['legacy_sources'].items()if h!=a['legacy_sources'][name]]
assert set(changes)=={'compiler/ssacapacityvalue.fern','compiler/ssalargepushranges.fern'}
expected=before
for name in changes:expected=expected.replace(b['legacy_sources'][name].encode(),a['legacy_sources'][name].encode())
assert expected==after
expected_records=[{'id':c['id'],'values':c['expected']}for c in a['cases']]
assert len(expected_records)==1566
workers=0;raw_files=0
for target,manifest in json.loads(read('worker-manifest.json')).items():
 r=json.loads(read(target+'.json'))
 assert r['qualified']and r['sources_unchanged']and r['compiler_unchanged']and r['records']==expected_records
 assert r['worker_binary_sha256_before_execution']==r['worker_binary_sha256_after_execution']
 assert len(r['workers'])==196
 for w in r['workers']:
  assert w['qualified']and w['exit']==0 and w['heap']['allocs']==w['heap']['frees']>0 and w['heap']['live_bytes']==0
 workers+=len(r['workers']);seen=set()
 with tarfile.open(fileobj=io.BytesIO(read(target+'-workers.tar.gz')),mode='r:gz')as archive:
  for member in archive.getmembers():
   path=PurePosixPath(member.name);assert member.isfile()and not path.is_absolute()and '..'not in path.parts
   assert member.name not in seen;seen.add(member.name)
   assert sha(archive.extractfile(member).read())==manifest['files'][member.name]
   raw_files+=1
 assert seen==set(manifest['files'])
assert workers==588
for name in ['result.json','build.json','ci-selftest-result.json','protocol-result.json','platforms-after.json','platforms-vet.json']:assert json.loads(read(name))['exit_code']==0,name
assert json.loads(read('platforms-before.json'))['exit_code']==1
assert json.loads(read('initial-compiler-failure/result.json'))['exit_code']==1
product='internal/testing/e2ecompiler/testdata/large_free_wide_classes.json.gz'
actual=subprocess.check_output(['git','show',args.commit+':'+product],cwd=ROOT)if args.commit else(ROOT/product).read_bytes()
assert actual==read('reference-current.json.gz')
print(json.dumps({'data_hashes_verified':len(m['sha256']),'raw_gzip_identities':len(json.loads(read('raw-identities.json'))),'worker_raw_files_verified':raw_files,'records':len(expected_records)*3,'balanced_workers':workers,'only_two_provenance_hashes_change':True}))
