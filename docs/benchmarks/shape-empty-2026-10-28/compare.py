from pathlib import Path
import gzip
import hashlib
import json
import re

p = Path(__file__).parent
baseline = p.parent / 'licm-no-loop-candidate'

def calls(folder, symbol):
    text = (folder / 'stage2.callers.resolved').read_text()
    block = next(b for b in text.split('\n\n') if re.search(r'\*.*' + re.escape(symbol) + r'(?:\+[^ @]+)?@', b))
    return sum(int(x.replace(',', '')) for x in re.findall(r'\(([\d,]+)x\)', block))

before = json.loads((baseline / 'candidate-profile-metrics.json').read_text())
after = json.loads((p / 'stage2-profile.json').read_text())
assert before['exit'] == after['exit'] == 0
assert before['valid_full_run'] and after['valid_full_run']
assert before['common_producer_sha256'] == after['common_producer_sha256'] == '1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52'
after['box_calls'] = calls(p, '__fern_arr_box')
(p / 'candidate-profile-metrics.json').write_text(json.dumps(after, indent=2) + '\n')
(p / 'baseline-profile-metrics.json').write_text(json.dumps(before, indent=2) + '\n')
outputs = []
for target in ['x86-64-linux', 'arm64-linux', 'wasm32-wasi']:
    name = 'checker-' + target + '.s'
    raw = gzip.decompress((baseline / (name + '.gz')).read_bytes())
    candidate = (p / name).read_bytes() if (p / name).exists() else gzip.decompress((p / (name + '.gz')).read_bytes())
    assert raw == candidate
    outputs.append(dict(target=target, bytes=len(raw), sha256=hashlib.sha256(raw).hexdigest(), byte_identical=True))
(p / 'output-identity.json').write_text(json.dumps(outputs, indent=2) + '\n')
comparison = dict(baseline=before, candidate=after, instruction_delta=after['instructions'] - before['instructions'], instruction_reduction_percent=100 * (1 - after['instructions'] / before['instructions']), box_delta=after['box_calls'] - before['box_calls'], output_identity_3targets=True)
(p / 'comparison.json').write_text(json.dumps(comparison, indent=2) + '\n')
print(json.dumps({k: v for k, v in comparison.items() if k not in ['baseline', 'candidate']}, indent=2))
