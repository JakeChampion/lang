"""Paired protocol and provenance faults cannot weaken numerical or lifecycle requirements."""
import copy
import gzip
import json
from pathlib import Path
import struct
import unittest

import qualify_icc_inverse as collector


def bits(value):return struct.unpack('<q',struct.pack('<d',value))[0]


class IccInverseProtocolTests(unittest.TestCase):
    def setUp(self):
        expected=dict(status='ok',polls=2,fraction=['1','2'])
        self.case=dict(name='half',kind=3,parameter=0,values=[131072],max_samples=4096,cancel_at=0,input_bits=bits(.25),expected=dict(dispatch=copy.deepcopy(expected),direct=copy.deepcopy(expected)))
        self.case['payload_sha256']=collector.sha(collector.packet(self.case))
        self.cancelled=copy.deepcopy(self.case);self.cancelled.update(name='cancelled',cancel_at=1,expected={r:dict(status='error',error='Cancelled',polls=1)for r in ['dispatch','direct']});self.cancelled['payload_sha256']=collector.sha(collector.packet(self.cancelled))
        self.cases=[self.case,self.cancelled];self.names=['case-0.icc-worker','case-1.icc-worker']
        self.stdout=''.join(f'iid {self.names[0]} {r} ok 2 {bits(.5)}\n'for r in ['dispatch','direct'])+''.join(f'iid {self.names[1]} {r} error Cancelled 1\n'for r in ['dispatch','direct'])
        self.stderr='leakcheck: allocs=29 frees=29 live_bytes=0\n'

    def check(self,stdout=None,stderr=None,cases=None,names=None,code=0):
        return collector.check_output(self.stdout if stdout is None else stdout,self.stderr if stderr is None else stderr,self.cases if cases is None else cases,self.names if names is None else names,code)

    def test_success_and_error(self):
        samples,census=self.check();self.assertEqual(samples[0]['observation']['dispatch']['result_bits'],bits(.5));self.assertEqual(samples[0]['observation']['direct']['result_bits'],bits(.5));self.assertEqual(census,dict(allocs=29,frees=29,live_bytes=0))

    def test_common_limit_difference_is_explicit(self):
        case=copy.deepcopy(self.case);case.update(max_samples=0);case['expected']['dispatch']=dict(status='error',error='Limit',polls=0);case['payload_sha256']=collector.sha(collector.packet(case))
        text=f'iid {self.names[0]} dispatch error Limit 0\niid {self.names[0]} direct ok 2 {bits(.5)}\n'
        self.check(cases=[case],names=self.names[:1],stdout=text)
        with self.assertRaises(ValueError):self.check(cases=[case],names=self.names[:1],stdout=text.replace('Limit 0','Cancelled 1'))

    def test_finite_criterion_and_nonfinite_results(self):
        for route in ['dispatch','direct']:
            for value in [.5+2e-12,.5-2e-12,float('nan'),float('inf'),-.01,1.01]:
                with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(f'{route} ok 2 {bits(.5)}',f'{route} ok 2 {bits(value)}'))

    def test_exact_bits(self):
        cases=copy.deepcopy(self.cases)
        for route in ['dispatch','direct']:cases[0]['expected'][route]['exact_bits']=bits(.5)
        with self.assertRaises(ValueError):self.check(cases=cases,stdout=self.stdout.replace(str(bits(.5)),str(bits(.5)+1)))

    def test_paired_success_must_match_bits(self):
        with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(f'direct ok 2 {bits(.5)}',f'direct ok 2 {bits(.5)+1}'))

    def test_errors_and_polls(self):
        for old,new in [('error Cancelled 1','error Domain 1'),('error Cancelled 1','error Cancelled 2'),('error Cancelled 1','error Numeric 1'),('ok 2','ok 3')]:
            with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(old,new))

    def test_missing_extra_reordered_and_duplicate_rows(self):
        lines=self.stdout.splitlines()
        for text in ['', '\n'.join(lines[:3])+'\n',self.stdout+'extra\n','\n'.join(lines[::-1])+'\n',self.stdout+'\n']:
            with self.assertRaises(ValueError):self.check(stdout=text)
        for names in [self.names[::-1],self.names[:1],[self.names[0]]*2]:
            with self.assertRaises(ValueError):self.check(names=names)

    def test_heap_requirements(self):
        for text in ['',self.stderr*2,'leakcheck: allocs=0 frees=0 live_bytes=0','leakcheck: allocs=29 frees=28 live_bytes=0','leakcheck: allocs=29 frees=29 live_bytes=1']:
            with self.assertRaises(ValueError):self.check(stderr=text)

    def test_process_and_sanitizer_failures(self):
        for code in [1,3,137,-9]:
            with self.assertRaises(ValueError):self.check(code=code)
        with self.assertRaises(ValueError):self.check(stderr=self.stderr+'fern-sanitizer: fault\n')

    def test_reference_identity_shape_and_numeric_waiver(self):
        for change in [dict(input_bits=bits(.75)),dict(kind=True),dict(cancel_at=-1),dict(expected=dict(dispatch=dict(status='error',error='Numeric',polls=0),direct=self.case['expected']['direct']))]:
            changed=copy.deepcopy(self.case);changed.update(change)
            with self.assertRaises(ValueError):self.check(cases=[changed,self.cancelled])
        changed=copy.deepcopy(self.case);changed['expected']['direct']['fraction']=['1','0']
        with self.assertRaises((ValueError,ZeroDivisionError)):self.check(cases=[changed,self.cancelled])

    def test_original_catalog_complete_and_bound(self):
        root=Path(__file__).resolve().parents[1];document=json.loads(gzip.decompress((root/'tests/fixtures/icc-inverse-reference.json.gz').read_bytes()))
        collector.audit_origins(document,root)
        removed=dict(document,cases=document['cases'][1:])
        with self.assertRaises(ValueError):collector.audit_origins(removed,root)
        altered=copy.deepcopy(document['cases'][0]);altered['input_bits']=bits(.75)
        changed=dict(document,cases=[altered,*document['cases'][1:]])
        with self.assertRaises(ValueError):collector.audit_origins(changed,root)

    def test_original_mathematical_expectation_cannot_change(self):
        root=Path(__file__).resolve().parents[1];document=json.loads(gzip.decompress((root/'tests/fixtures/icc-inverse-reference.json.gz').read_bytes()))
        altered=copy.deepcopy(document['cases'][0]);altered['expected']['dispatch']['exact_bits']=bits(.5)
        changed=dict(document,cases=[altered,*document['cases'][1:]])
        with self.assertRaises(ValueError):collector.audit_origins(changed,root)


if __name__=='__main__':unittest.main()
