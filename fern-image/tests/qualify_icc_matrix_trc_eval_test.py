"""Eight fault groups for numeric stages, CMM, protocol, references, and cleanup."""
import copy
from fractions import Fraction
import math
import unittest
import qualify_icc_matrix_trc_eval as c
from generate_icc_matrix_trc_eval import curve, expected, bits, value, packet, frozen_model


def make_case(name='ordinary',frozen=False):
    matrix,curves=frozen_model()if frozen else([32768,0,0,0,32768,0,0,0,32768],[curve(1,512)]*3)
    case=dict(name=name,requested=1,max_samples=4096,cancel_at=0,frozen=frozen,cmm_applicable=frozen,
              matrix=matrix,curves=curves,input_bits=[bits(.5)]*3)
    case['expected']=expected(matrix,curves,case['input_bits'],0)
    case['payload_sha256']=c.sha(packet(case))
    return case


def numeric_row(case,name,trace=None,xyz=None):
    e=case['expected']
    if trace is None:trace=[float(c.numeric_reference(r))for r in e['linear']]
    if xyz is None:xyz=[float(sum((Fraction(a,65536)*Fraction(v)for a,v in zip(case['matrix'][3*i:3*i+3],trace)),Fraction()))for i in range(3)]
    return 'composition '+name+' ok '+str(e['polls'])+' '+' '.join(str(bits(v))for v in trace+xyz)+'\n'


class CompositionProtocolTests(unittest.TestCase):
    def setUp(self):
        self.case=make_case();self.error=copy.deepcopy(self.case);self.error.update(name='cancelled',cancel_at=1)
        self.error['expected']=dict(status='error',error='Cancelled',polls=1);self.error['payload_sha256']=c.sha(packet(self.error))
        self.cases=[self.case,self.error];self.names=['case-0.icc-worker','case-1.icc-worker']
        self.stdout=numeric_row(self.case,self.names[0])+'composition '+self.names[1]+' error Cancelled 1\n'
        self.stderr='leakcheck: allocs=19 frees=19 live_bytes=0\n'
    def check(self,stdout=None,stderr=None,cases=None,names=None,code=0,cmm=None):
        return c.check_output(self.stdout if stdout is None else stdout,self.stderr if stderr is None else stderr,
                              self.cases if cases is None else cases,self.names if names is None else names,code,cmm)
    def test_success(self):
        samples,census=self.check();self.assertEqual(census['live_bytes'],0);self.assertEqual(samples[0]['observation']['matrix_representable_steps'],[0]*3)
    def test_curve_accuracy_and_identity(self):
        self.check(stdout=numeric_row(self.case,self.names[0],[.25+2e-13]*3)+self.stdout.splitlines(True)[1])
        with self.assertRaisesRegex(ValueError,'curve criterion'):self.check(stdout=numeric_row(self.case,self.names[0],[.25+2e-12]*3)+self.stdout.splitlines(True)[1])
        case=copy.deepcopy(self.case);case['curves']=[curve()]*3;case['input_bits']=[bits(-0.0)]*3;case['matrix']=[65536,0,0,0,65536,0,0,0,65536]
        case['expected']=expected(case['matrix'],case['curves'],case['input_bits'],0);case['payload_sha256']=c.sha(packet(case))
        text='composition '+self.names[0]+' ok 10 '+' '.join([str(bits(-0.0))]*6)+'\n'
        self.check(stdout=text+self.stdout.splitlines(True)[1],cases=[case,self.error])
        with self.assertRaisesRegex(ValueError,'identity bits'):self.check(stdout=text.replace(str(bits(-0.0)),'0')+self.stdout.splitlines(True)[1],cases=[case,self.error])
    def test_matrix_steps_and_exact_projections(self):
        raw=bits(.125)
        for delta in [-2,-1,1,2]:self.check(stdout=self.stdout.replace(str(raw),str(raw+delta)))
        for delta in [-3,3]:
            with self.assertRaisesRegex(ValueError,'matrix criterion'):self.check(stdout=self.stdout.replace(str(raw),str(raw+delta)))
        case=copy.deepcopy(self.case);case['matrix']=[65536,0,0,0,65536,0,0,0,65536];case['expected']=expected(case['matrix'],case['curves'],case['input_bits'],0);case['payload_sha256']=c.sha(packet(case))
        text=numeric_row(case,self.names[0]);parts=text.split(' ');parts[7]=str(bits(.25)+1);text=' '.join(parts)
        with self.assertRaisesRegex(ValueError,'projection bits'):self.check(stdout=text+self.stdout.splitlines(True)[1],cases=[case,self.error])
    def test_full_ideal_propagated_criterion(self):
        case=copy.deepcopy(self.case);case['expected']['ideal_xyz'][0]='0.125001'
        with self.assertRaisesRegex(ValueError,'composed criterion'):self.check(cases=[case,self.error])
    def test_live_and_frozen_cmm(self):
        case=make_case('frozen',True);text=numeric_row(case,self.names[0]);observed=list(map(int,text.split()[7:]));case['expected']['cmm_xyz_bits']=observed
        self.check(stdout=text+self.stdout.splitlines(True)[1],cases=[case,self.error],cmm={'frozen':observed})
        with self.assertRaisesRegex(ValueError,'missing live CMM'):self.check(stdout=text+self.stdout.splitlines(True)[1],cases=[case,self.error])
        bad=observed.copy();bad[0]=bits(value(bad[0])+.01)
        with self.assertRaisesRegex(ValueError,'CMM criterion'):self.check(stdout=text+self.stdout.splitlines(True)[1],cases=[case,self.error],cmm={'frozen':bad})
        for bad in [[bits(math.nan)]*3,[True]*3,[]]:
            with self.assertRaises(ValueError):c.check_cmm([0.,0.,0.],bad)
    def test_errors_and_polls(self):
        for text in ['error Shape 1','error Cancelled 2','error Unknown 1']:
            with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace('error Cancelled 1',text))
        with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace('ok 10','ok 9'))
    def test_rows_canonical_numbers_and_nonfinite(self):
        lines=self.stdout.splitlines(True)
        for text in ['',lines[0],self.stdout+'extra\n',''.join(reversed(lines)),self.stdout+'\n']:
            with self.assertRaises(ValueError):self.check(stdout=text)
        for names in [self.names[::-1],[self.names[0]]*2,self.names[:1]]:
            with self.assertRaises(ValueError):self.check(names=names)
        for token in ['+'+str(bits(.25)),'0'+str(bits(.25)),str(bits(math.inf)),str(bits(math.nan))]:
            with self.assertRaises(ValueError):self.check(stdout=self.stdout.replace(str(bits(.25)),token))
    def test_process_sanitizer_and_heap(self):
        for code in [1,3,137,-9]:
            with self.assertRaises(ValueError):self.check(code=code)
        for text in ['',self.stderr*2,'leakcheck: allocs=19 frees=18 live_bytes=0','leakcheck: allocs=19 frees=19 live_bytes=1','leakcheck: allocs=0 frees=0 live_bytes=0',self.stderr+'fern-sanitizer: fault\n']:
            with self.assertRaises(ValueError):self.check(stderr=text)
    def test_reference_identity_configuration_and_shape(self):
        changes=[dict(cancel_at=True),dict(max_samples=True),dict(matrix=[2**31]*9),dict(input_bits=[bits(.75)]*3),dict(curves=[curve()]),dict(cmm_applicable=True)]
        for change in changes:
            case=copy.deepcopy(self.case);case.update(change)
            with self.assertRaises(ValueError):self.check(cases=[case,self.error])
        for change in [dict(polls=True),dict(ideal_xyz=['NaN']*3),dict(linear=[{'fraction':['1','0']}]*3),dict(exact_xyz_bits=[2**63]*3)]:
            case=copy.deepcopy(self.case);case['expected'].update(change)
            with self.assertRaises(ValueError):self.check(cases=[case,self.error])


if __name__=='__main__':unittest.main()
