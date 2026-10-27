"""Reject numerical, protocol, reference, process, and heap faults."""
import copy
from fractions import Fraction
import unittest
import qualify_icc_matrix_solve as c
from generate_icc_matrix_solve import bits, expected

class SolveProtocolTests(unittest.TestCase):
    def setUp(self):
        matrix=[65536,0,0,0,65536,0,0,0,65536]
        self.case=dict(name='identity',coefficients=matrix,input_bits=[bits(0.5)]*3,cancel_at=0)
        self.case['expected']=expected(matrix,self.case['input_bits'],0)
        self.case['payload_sha256']=c.sha(c.packet(self.case))
        self.error=copy.deepcopy(self.case);self.error.update(name='cancelled',cancel_at=1)
        self.error['expected']=expected(matrix,self.case['input_bits'],1)
        self.error['payload_sha256']=c.sha(c.packet(self.error))
        self.cases=[self.case,self.error];self.names=['case-0.icc-worker','case-1.icc-worker']
        self.stdout=f'solve {self.names[0]} ok 2 {bits(0.5)} {bits(0.5)} {bits(0.5)}\nsolve {self.names[1]} error Cancelled 1\n'
        self.stderr='leakcheck: allocs=19 frees=19 live_bytes=0\n'
    def check(self,stdout=None,stderr=None,cases=None,names=None,code=0):
        return c.check_output(self.stdout if stdout is None else stdout,self.stderr if stderr is None else stderr,self.cases if cases is None else cases,self.names if names is None else names,code)
    def test_success_and_error(self):
        samples,census=self.check();self.assertEqual(samples[0]['observation']['representable_steps'],[0]*3);self.assertEqual(census['live_bytes'],0)
    def test_two_steps_and_exact_bits(self):
        cases=copy.deepcopy(self.cases);cases[0]['expected']['exact_bits']=[None]*3
        for delta in [-2,-1,1,2]:self.check(cases=cases,stdout=self.stdout.replace(str(bits(0.5)),str(bits(0.5)+delta)))
        for delta in [-3,3]:
            with self.assertRaises(ValueError):self.check(cases=cases,stdout=self.stdout.replace(str(bits(0.5)),str(bits(0.5)+delta)))
        with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(str(bits(0.5)),str(bits(0.5)+1)))
    def test_nonfinite_and_signed_order(self):
        for value in [float('inf'),float('nan'),-0.5]:
            with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(str(bits(0.5)),str(bits(value))))
        self.assertEqual(c.order(bits(-0.0)),c.order(bits(0.0)))
        self.assertEqual(c.order(bits(-5e-324)),-1)
        self.assertEqual(c.order(bits(5e-324)),1)
    def test_errors_and_polls(self):
        for text in ['error Shape 1','error Cancelled 2','error Unknown 1']:
            with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace('error Cancelled 1',text))
        with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace('ok 2','ok 1'))
    def test_missing_extra_reordered_and_duplicate(self):
        lines=self.stdout.splitlines()
        for text in ['',lines[0]+'\n',self.stdout+'extra\n','\n'.join(reversed(lines))+'\n',self.stdout+'\n']:
            with self.assertRaises(ValueError):self.check(stdout=text)
        for names in [self.names[::-1],self.names[:1],[self.names[0]]*2]:
            with self.assertRaises(ValueError):self.check(names=names)
    def test_heap_requirements(self):
        for text in ['',self.stderr*2,'leakcheck: allocs=19 frees=18 live_bytes=0','leakcheck: allocs=19 frees=19 live_bytes=1','leakcheck: allocs=0 frees=0 live_bytes=0']:
            with self.assertRaises(ValueError):self.check(stderr=text)
    def test_process_and_sanitizer(self):
        for code in [1,3,137,-9]:
            with self.assertRaises(ValueError):self.check(code=code)
        with self.assertRaises(ValueError):self.check(stderr=self.stderr+'fern-sanitizer: fault\n')
    def test_admission_reference_precedes_cancellation(self):
        for coefficients, inputs, error in [([0]*9, [bits(0.5)]*3, 'Singular'), ([98305]+[0]*8, [bits(0.5)]*3, 'Unsupported'), ([98305]+[0]*8, [bits(float('inf'))]*3, 'Input'), ([], [bits(float('inf'))]*3, 'Shape')]:
            case=dict(name='admission',coefficients=coefficients,input_bits=inputs,cancel_at=1)
            case['expected']=dict(status='error',error=error,polls=0)
            self.assertEqual(expected(coefficients,inputs,1),case['expected'])
            case['payload_sha256']=c.sha(c.packet(case))
            self.check(cases=[case],names=[self.names[0]],stdout=f'solve {self.names[0]} error {error} 0\n')
            for wrong in ['Cancelled 1','Numeric 0',error+' 1']:
                with self.assertRaises(ValueError):self.check(cases=[case],names=[self.names[0]],stdout=f'solve {self.names[0]} error {wrong}\n')
    def test_reference_identity_and_shape(self):
        for change in [dict(input_bits=[bits(0.75)]*3),dict(cancel_at=True),dict(coefficients=[2**31]*9),dict(expected=dict(status='ok',polls=2,fractions=[['1','0']]*3,exact_bits=[None]*3)),dict(expected=dict(status='error',error='Unknown',polls=0)),dict(expected=dict(status='error',error='Numeric',polls=0))]:
            case=copy.deepcopy(self.case);case.update(change)
            with self.assertRaises((ValueError,ZeroDivisionError)):self.check(cases=[case,self.error])

if __name__=='__main__':unittest.main()
