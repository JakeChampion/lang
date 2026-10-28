"""Exercise exact decoder records and reject complete protocol defects."""
import argparse
import copy
import hashlib
from pathlib import Path
import tempfile
import unittest

from qualify_large_push_wide_decoder import GEN, check_output, validate_case, run


def case(ordinal, body):
    ops = GEN.decode_body(body)
    item = dict(id=ordinal, name='fixture-' + str(ordinal), source=GEN.envelope(body), expected=dict(ok=ops is not None, ops=[] if ops is None else ops))
    item['payload_sha256'] = hashlib.sha256(GEN.packet(item)).hexdigest()
    return item


def wire(cases):
    def encoded(s):
        return s.encode().hex() if s else '-'
    lines = []
    for c in cases:
        if c['expected']['ok']:
            lines.append(f'wid {c["id"]} ok {len(c["expected"]["ops"])}')
            for at, op in enumerate(c['expected']['ops']):
                fields = [str(op[key]) for key in GEN.FIELDS[:10]] + [encoded(op['condition']), str(op['target']), encoded(op['symbol'])]
                lines.append(' '.join(['op', str(c['id']), str(at), *fields]))
        else:
            lines.append(f'wid {c["id"]} error 0 ' + 'unsupported instruction'.encode().hex())
        lines.append('end ' + str(c['id']))
    return '\n'.join(lines) + '\n'


class Protocol(unittest.TestCase):
    def setUp(self):
        self.cases = [case(0, 'movabs $17179869184, %r15\nret'), case(1, 'unknown_instruction\nret'), case(2, '.Lcycle:\njmp .Lcycle')]
        self.stdout = wire(self.cases)
        self.stderr = 'leakcheck: allocs=64 frees=64 live_bytes=0\n'
        records, heap = check_output(self.stdout, self.stderr, self.cases)
        self.assertEqual(records[0]['ops'][0]['immediate'], 17179869184)
        self.assertEqual(heap['live_bytes'], 0)

    def bad(self, stdout=None, stderr=None, cases=None, code=0):
        with self.assertRaises(ValueError):
            check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr, self.cases if cases is None else cases, code)

    def test_complete_success_error_and_cycle(self):
        records, _ = check_output(self.stdout, self.stderr, self.cases)
        self.assertEqual([r['id'] for r in records], [0, 1, 2])
        self.assertFalse(records[1]['ok'])
        self.assertEqual(records[2]['ops'][1]['target'], 0)

    def test_nonzero_exit_rejects_even_complete_output(self):
        for code in [1, 2, 180, -9]:
            with self.subTest(code=code):
                self.bad(code=code)

    def test_heap_faults(self):
        for text in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0\n', 'leakcheck: allocs=64 frees=63 live_bytes=0\n', 'leakcheck: allocs=64 frees=64 live_bytes=1\n', self.stderr * 2, self.stderr + 'fern-sanitizer: fault\n', 'extra\n' + self.stderr]:
            with self.subTest(text=text):
                self.bad(stderr=text)

    def test_missing_extra_duplicate_reordered_cases(self):
        self.bad(stdout=wire(self.cases[:-1]))
        self.bad(stdout=self.stdout + wire(self.cases[:1]))
        self.bad(stdout=wire([self.cases[0], self.cases[0], self.cases[2]]))
        self.bad(stdout=wire(list(reversed(self.cases))))
        self.bad(cases=[self.cases[0], self.cases[0], self.cases[2]])

    def test_every_operation_field_is_exact(self):
        original = self.stdout.splitlines()
        for pos in range(3, 16):
            lines = original.copy()
            fields = lines[1].split(' ')
            fields[pos] = '6a61' if pos in [13, 15] else str(int(fields[pos]) + 1)
            lines[1] = ' '.join(fields)
            with self.subTest(field=pos):
                self.bad(stdout='\n'.join(lines) + '\n')
        self.bad(stdout=self.stdout.replace('17179869184', '0', 1))

    def test_operation_shape_and_ordinals(self):
        for old, new in [('op 0 0 ', 'op 0 1 '), ('op 0 0 ', 'op 1 0 '), ('op 0 0 ', 'op  0 0 '), ('41 64', '41.0 64'), ('41 64', '041 64'), ('41 64', 'true 64')]:
            with self.subTest(new=new):
                self.bad(stdout=self.stdout.replace(old, new, 1))
        lines = self.stdout.splitlines()
        self.bad(stdout='\n'.join(lines[:1] + lines[2:]) + '\n')
        self.bad(stdout='\n'.join(lines[:2] + lines[1:]) + '\n')

    def test_partial_prefix_and_case_end(self):
        for text in ['\n'.join(self.stdout.splitlines()[:2]) + '\n', self.stdout.replace('end 0\n', '', 1), self.stdout.replace('end 2', 'end 9'), self.stdout + '\n']:
            with self.subTest(text=text):
                self.bad(stdout=text)

    def test_error_has_no_operations_or_waiver(self):
        marker = 'wid 1 error 0 '
        for replacement in ['wid 1 ok 0 ', 'wid 1 error 1 ', 'wid 1 Numeric 0 ']:
            self.bad(stdout=self.stdout.replace(marker, replacement, 1))
        lines = self.stdout.splitlines()
        for encoded in ['-', 'zz', 'ff']:
            changed = [marker + encoded if line.startswith(marker) else line for line in lines]
            self.bad(stdout='\n'.join(changed) + '\n')

    def test_reference_shape_and_packet_binding(self):
        for field, value in [('id', True), ('source', 17), ('payload_sha256', '0' * 64)]:
            candidate = copy.deepcopy(self.cases[0])
            candidate[field] = value
            with self.subTest(field=field), self.assertRaises((ValueError, TypeError)):
                validate_case(candidate)
        candidate = copy.deepcopy(self.cases[0])
        candidate['expected']['ops'][0]['immediate'] = float(17179869184)
        with self.assertRaises(ValueError):
            validate_case(candidate)
        candidate = copy.deepcopy(self.cases[1])
        candidate['expected']['ops'] = self.cases[0]['expected']['ops']
        with self.assertRaises(ValueError):
            validate_case(candidate)

    def test_changed_reference_value_rejects_actual_output(self):
        changed = copy.deepcopy(self.cases)
        changed[0]['expected']['ops'][0]['immediate'] = 0
        self.bad(cases=changed)

    def test_packet_golden(self):
        payload = validate_case(self.cases[0])
        self.assertEqual(payload[:4], b'FWI1')
        self.assertEqual(int.from_bytes(payload[4:8], 'little'), 0)
        self.assertEqual(int.from_bytes(payload[8:12], 'little'), len(payload[12:]))
        self.assertEqual(payload[12:].decode(), self.cases[0]['source'])

    def test_existing_output_and_work_are_preserved(self):
        with tempfile.TemporaryDirectory() as folder:
            output = Path(folder) / 'record.json'
            output.write_text('existing evidence')
            work = Path(folder) / 'work'
            with self.assertRaises(ValueError):
                run(argparse.Namespace(output=str(output), work=str(work)))
            self.assertEqual(output.read_text(), 'existing evidence')
            output = Path(folder) / 'new.json'
            work.mkdir()
            with self.assertRaises(ValueError):
                run(argparse.Namespace(output=str(output), work=str(work)))
            self.assertFalse(output.exists())


if __name__ == '__main__':
    unittest.main()
