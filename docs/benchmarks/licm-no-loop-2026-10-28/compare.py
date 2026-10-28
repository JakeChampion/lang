from pathlib import Path
import json
import re

p=Path(__file__).parent
baseline=p.parent/'phi-predecessor-candidate'

def calls(folder,symbol):
    text=(folder/'stage2.callers.resolved').read_text()
    block=next(b for b in text.split('\n\n') if re.search(r'\*.*'+re.escape(symbol)+r'(?:\+[^ @]+)?@',b))
    return dict(calls=sum(int(x.replace(',','')) for x in re.findall(r'\(([\d,]+)x\)',block)),callers=[line.strip() for line in block.splitlines() if '<' in line])

before=json.loads((baseline/'candidate-profile-metrics.json').read_text())
after=json.loads((p/'stage2-profile.json').read_text())
assert before['exit']==after['exit']==0
assert before['valid_full_run'] and after['valid_full_run']
assert before['common_producer_sha256']==after['common_producer_sha256']=='1e0ad6be807a01bbf50a05535d46808d611816016ce388e687d44589c5dc7e52'
after['box_calls']=calls(p,'__fern_arr_box')['calls']
(p/'candidate-profile-metrics.json').write_text(json.dumps(after,indent=2)+'\n')
(p/'baseline-profile-metrics.json').write_text(json.dumps(before,indent=2)+'\n')
comparison=dict(baseline=before,candidate=after,instruction_delta=after['instructions']-before['instructions'],instruction_reduction_percent=100*(1-after['instructions']/before['instructions']),box_delta=after['box_calls']-before['box_calls'],slot_scans={name:calls(folder,'__fn_ir__cp_max_slot') for name,folder in [('baseline',baseline),('candidate',p)]})
(p/'comparison.json').write_text(json.dumps(comparison,indent=2)+'\n')
print(json.dumps(comparison,indent=2))
