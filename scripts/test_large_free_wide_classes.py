"""Reject incomplete class records, false references, and invalid heap reports."""
import copy
import unittest

from qualify_large_free_wide_classes import GEN, check_output, validate_case


class Protocol(unittest.TestCase):
    def setUp(self):
        self.cases = GEN.catalog()['cases'][:3]
        self.output = ''.join('class ' + str(c['id']) + ' ' + ' '.join(map(str, c['expected'])) + '\nend ' + str(c['id']) + '\n' for c in self.cases)
        self.heap = 'leakcheck: allocs=64 frees=64 live_bytes=0\n'

    def reject(self, output=None, heap=None, cases=None, exit=0):
        with self.assertRaises(ValueError):
            check_output(self.output if output is None else output, self.heap if heap is None else heap, self.cases if cases is None else cases, exit)

    def test_exact(self):
        self.assertEqual(len(check_output(self.output, self.heap, self.cases)[0]), 3)

    def test_exit(self):
        for code in [1, 2, 180, -9]:
            self.reject(exit=code)

    def test_heap(self):
        for heap in ['', self.heap * 2, self.heap.replace('frees=64', 'frees=63'), self.heap.replace('live_bytes=0', 'live_bytes=1'), 'leakcheck: allocs=0 frees=0 live_bytes=0\n', self.heap + 'extra\n']:
            self.reject(heap=heap)

    def test_complete(self):
        for output in [self.output[:-1], self.output + '\n', '\n'.join(self.output.splitlines()[:-2]) + '\n', self.output + self.output, self.output.replace('end 0', 'end 1')]:
            self.reject(output=output)

    def test_integer_grammar(self):
        for token in ['+0', '00', '0.0', 'true', 'unsupported', '']:
            self.reject(output=self.output.replace('class 0 0 ', 'class 0 ' + token + ' ', 1))

    def test_identity(self):
        self.reject(cases=[self.cases[0]] * 3)
        self.reject(cases=list(reversed(self.cases)))
        for field, value in [('id', True), ('input', [0, True, 0, 0, 0]), ('expected', [False, 0, 0, 0, 0, 0]), ('payload_sha256', '0' * 64)]:
            case = copy.deepcopy(self.cases[0])
            case[field] = value
            with self.assertRaises(ValueError):
                validate_case(case)

    def test_wrong_result(self):
        self.reject(output=self.output.replace('class 0 0 ', 'class 0 1 ', 1))

    def test_domains_and_refinement(self):
        ref = GEN.catalog()
        self.assertEqual([len(ref['grids'][str(m)]['current']) for m in range(3)], [70, 103, 103])
        self.assertEqual(GEN.classify(0, 536870909, 536870909, 0, 0, GEN.CAPS), 52)
        self.assertEqual(GEN.classify(2, 2 ** 61 - 4, 2 ** 61 - 4, 0, 7, GEN.CAPS), -2)
        self.assertEqual(ref['grids']['1']['current'][-1], [2 ** 60, 2 ** 60, 1, 7])


if __name__ == '__main__':
    unittest.main()
