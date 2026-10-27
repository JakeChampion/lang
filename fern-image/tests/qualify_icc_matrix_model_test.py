"""Protocol faults must retain exact data, process, and heap requirements."""
import copy
import unittest
import qualify_icc_matrix_model as c
from generate_icc_matrix_model import components, build
import base64

class ModelProtocolTests(unittest.TestCase):
    def setUp(self):
        entries,columns,curves=components();raw,meta=build(entries,columns,curves)
        self.case=dict(name='model',requested=1,limits=[1048576,128,1048576,4096],cancel_at=0,input_base64=base64.b64encode(raw).decode(),expected=dict(status='ok',polls=meta['polls'],values=meta['tokens']))
        self.case['payload_sha256']=c.sha(c.packet(self.case))
        self.error=copy.deepcopy(self.case);self.error.update(name='intent',requested=0,expected=dict(status='error',error='Intent',polls=0));self.error['payload_sha256']=c.sha(c.packet(self.error))
        self.cases=[self.case,self.error];self.names=['case-0.icc-worker','case-1.icc-worker']
        self.line='model '+self.names[0]+' ok '+str(meta['polls'])+' '+' '.join(map(str,meta['tokens']))
        self.stdout=self.line+'\nmodel '+self.names[1]+' error Intent 0\n';self.stderr='leakcheck: allocs=31 frees=31 live_bytes=0\n'
    def check(self,stdout=None,stderr=None,cases=None,names=None,code=0):return c.check_output(self.stdout if stdout is None else stdout,self.stderr if stderr is None else stderr,self.cases if cases is None else cases,self.names if names is None else names,code)
    def test_success_and_error(self):
        samples,heap=self.check();self.assertEqual(samples[0]['observation']['checked_integers'],len(self.case['expected']['values']));self.assertEqual(samples[0]['observation']['observed_values_sha256'],c.sha(c.canonical(self.case['expected']['values'])));self.assertEqual(heap['live_bytes'],0)
    def test_every_returned_integer(self):
        fields=self.line.split(' ')
        for i in [0,14,len(self.case['expected']['values'])//2,len(self.case['expected']['values'])-1]:
            changed=fields.copy();changed[4+i]=str(int(changed[4+i])+1)
            with self.assertRaises(ValueError):self.check(stdout=' '.join(changed)+'\n'+self.stdout.splitlines()[1]+'\n')
    def test_canonical_values_and_polls(self):
        fields=self.line.split(' ')
        for v in ['01','+1','nan',str(2**63),'1.0']:
            changed=fields.copy();changed[-1]=v
            with self.assertRaises(ValueError):self.check(stdout=' '.join(changed)+'\n'+self.stdout.splitlines()[1]+'\n')
        for old,new in [('ok '+str(self.case['expected']['polls']),'ok 0'),('error Intent 0','error Intent 1'),('error Intent 0','error Unknown 0')]:
            with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(old,new))
    def test_missing_extra_reordered_and_duplicate(self):
        lines=self.stdout.splitlines()
        for text in ['',lines[0]+'\n',self.stdout+'extra\n','\n'.join(reversed(lines))+'\n',self.stdout+'\n']:
            with self.assertRaises(ValueError):self.check(stdout=text)
        for names in [self.names[::-1],self.names[:1],[self.names[0]]*2]:
            with self.assertRaises(ValueError):self.check(names=names)
    def test_heap_requirements(self):
        for text in ['',self.stderr*2,'leakcheck: allocs=31 frees=30 live_bytes=0','leakcheck: allocs=31 frees=31 live_bytes=1','leakcheck: allocs=0 frees=0 live_bytes=0']:
            with self.assertRaises(ValueError):self.check(stderr=text)
    def test_process_and_sanitizer(self):
        for code in [1,3,137,-9]:
            with self.assertRaises(ValueError):self.check(code=code)
        with self.assertRaises(ValueError):self.check(stderr=self.stderr+'fern-sanitizer: failure\n')
    def test_reference_identity_and_shapes(self):
        for change in [dict(requested=True),dict(requested=4),dict(limits=[True,128,1,1]),dict(cancel_at=-1),dict(input_base64='!invalid'),dict(expected=dict(status='error',error='Unknown',polls=0)),dict(expected=dict(status='ok',polls=0,values=[0]))]:
            changed=copy.deepcopy(self.case);changed.update(change)
            with self.assertRaises((ValueError,TypeError)):self.check(cases=[changed,self.error])
    def test_long_interior_is_compared(self):
        case=copy.deepcopy(self.case);case['expected']['values']=list(range(2000));values=case['expected']['values'];line='model '+self.names[0]+' ok '+str(case['expected']['polls'])+' '+' '.join(map(str,values))
        text=line+'\n'+self.stdout.splitlines()[1]+'\n';samples,_=self.check(stdout=text,cases=[case,self.error]);self.assertEqual(samples[0]['observation']['checked_integers'],2000)
        fields=line.split(' ');fields[4+1234]='9999'
        with self.assertRaises(ValueError):self.check(stdout=' '.join(fields)+'\n'+self.stdout.splitlines()[1]+'\n',cases=[case,self.error])

if __name__=='__main__':unittest.main()
