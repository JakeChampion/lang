"""Require exact complete proof records and reject protocol defects."""
import argparse
import copy
import hashlib
from pathlib import Path
import tempfile
import unittest

from qualify_large_push_wide_machine import GEN, check_output, validate_case, run


def case(ordinal, expected):
    c = dict(id=ordinal, name='fixture-' + str(ordinal), source='arbitrary controlled source ' + str(ordinal), expected=expected, legacy_provenance=None)
    c['payload_sha256'] = hashlib.sha256(GEN.packet(c)).hexdigest()
    return c


def wire(cases):
    return ''.join('proof ' + str(c['id']) + ' ' + str(int(c['expected'])) + '\nend ' + str(c['id']) + '\n' for c in cases)


class Protocol(unittest.TestCase):
    def setUp(self):
        self.cases = [case(0, True), case(1, False), case(2, True)]
        self.stdout = wire(self.cases)
        self.stderr = 'leakcheck: allocs=64 frees=64 live_bytes=0\n'
        records, heap = check_output(self.stdout, self.stderr, self.cases)
        self.assertEqual([c['proved'] for c in records], [True, False, True])
        self.assertEqual(heap['live_bytes'], 0)

    def bad(self, stdout=None, stderr=None, cases=None, code=0):
        with self.assertRaises(ValueError):
            check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr, self.cases if cases is None else cases, code)

    def test_complete_positive_and_negative_records(self):
        self.assertEqual(len(check_output(self.stdout, self.stderr, self.cases)[0]), 3)

    def test_nonzero_exit(self):
        for code in [1, 2, 180, -9]:
            self.bad(code=code)

    def test_heap_contract(self):
        for text in ['', self.stderr * 2, self.stderr.replace('frees=64', 'frees=63'), self.stderr.replace('live_bytes=0', 'live_bytes=1'), 'leakcheck: allocs=0 frees=0 live_bytes=0\n', self.stderr + 'fern-sanitizer: fault\n', 'extra\n' + self.stderr]:
            self.bad(stderr=text)

    def test_missing_extra_duplicate_reordered_cases(self):
        self.bad(stdout=wire(self.cases[:-1]))
        self.bad(stdout=self.stdout + wire(self.cases[:1]))
        self.bad(stdout=wire([self.cases[0], self.cases[0], self.cases[2]]))
        self.bad(stdout=wire(list(reversed(self.cases))))
        self.bad(cases=[self.cases[0], self.cases[0], self.cases[2]])

    def test_accepting_a_fault_is_rejected(self):
        self.bad(stdout=self.stdout.replace('proof 1 0', 'proof 1 1'))

    def test_rejecting_a_valid_proof_is_rejected(self):
        self.bad(stdout=self.stdout.replace('proof 0 1', 'proof 0 0'))

    def test_boolean_grammar_and_waivers(self):
        for token in ['01', '+1', '1.0', 'true', 'True', '2', '-1', 'Numeric', 'unsupported', '']:
            self.bad(stdout=self.stdout.replace('proof 0 1', 'proof 0 ' + token))
        self.bad(stdout=self.stdout.replace('proof 0 1', 'proof 00 1'))
        self.bad(stdout=self.stdout.replace('proof 0 1', 'proof  0 1'))

    def test_partial_prefix_end_and_extra_output(self):
        for text in ['proof 0 1\n', 'proof 0 1\nend 0\n', self.stdout.replace('end 0', 'end 9'), self.stdout + '\n', self.stdout + 'extra\n']:
            self.bad(stdout=text)

    def test_reference_shape_and_packet_binding(self):
        for field, value in [('id', True), ('source', 17), ('expected', 1), ('payload_sha256', '0' * 64)]:
            c = copy.deepcopy(self.cases[0]);c[field] = value
            with self.assertRaises((ValueError, TypeError)):
                validate_case(c)
        c = copy.deepcopy(self.cases[0]);c['legacy_provenance'] = dict(original_expected='true')
        with self.assertRaises(ValueError):
            validate_case(c)

    def test_changed_expectation_rejects_actual_output(self):
        changed = copy.deepcopy(self.cases);changed[0]['expected'] = False
        self.bad(cases=changed)

    def test_packet_golden(self):
        raw = validate_case(self.cases[0])
        self.assertEqual(raw[:4], b'FWP1')
        self.assertEqual(int.from_bytes(raw[4:8], 'little'), 0)
        self.assertEqual(int.from_bytes(raw[8:12], 'little'), len(raw[12:]))
        self.assertEqual(raw[12:].decode(), self.cases[0]['source'])

    def test_existing_outputs_are_preserved(self):
        with tempfile.TemporaryDirectory() as folder:
            p = Path(folder);out = p / 'record.json';out.write_text('original evidence');work = p / 'work'
            with self.assertRaises(ValueError):
                run(argparse.Namespace(output=str(out), work=str(work)))
            self.assertEqual(out.read_text(), 'original evidence')
            work.mkdir()
            with self.assertRaises(ValueError):
                run(argparse.Namespace(output=str(p / 'new.json'), work=str(work)))
            self.assertFalse((p / 'new.json').exists())


if __name__ == '__main__':
    unittest.main()
