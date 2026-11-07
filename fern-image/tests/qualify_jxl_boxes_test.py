"""Reject incomplete or contradictory box qualification evidence."""

import struct
import unittest

import qualify_jxl_boxes as qualification


class BoxEvidenceTests(unittest.TestCase):
    heap = 'leakcheck: allocs=7 frees=7 live_bytes=0\n'

    def check(self, output='0 1 ok 1 1952805748:Short:0:8:3:11,\n', heap=None):
        qualification.check_worker(output, self.heap if heap is None else heap,
                                   [dict(data=qualification.envelope(b'test', b'abc'))], [1])

    def test_exact_descriptor(self):
        self.check()

    def test_all_length_forms(self):
        for form, width in [('Short', 8), ('Extended', 16), ('ToEnd', 8)]:
            with self.subTest(form=form):
                value = qualification.envelope(b'test', b'abc', form)
                self.assertEqual(qualification.inspect(value),
                                 dict(headers=[(1952805748, form, 0, width, 3, width + 3)]))

    def test_unsigned_extended_lengths(self):
        for length in [0, 1, 7, 8, 15]:
            with self.subTest(length=length):
                self.assertEqual(qualification.inspect(struct.pack('>I4sQ', 1, b'test', length)), dict(error='Length'))
        for length in [17, 2**31, 2**32, 2**63, 2**64 - 1]:
            with self.subTest(length=length):
                self.assertEqual(qualification.inspect(struct.pack('>I4sQ', 1, b'test', length)), dict(error='Truncated'))

    def test_zero_size_consumes_remaining_bytes(self):
        nested = qualification.envelope(b'test', b'abc')
        self.assertEqual(qualification.inspect(qualification.envelope(b'uuid', nested, 'ToEnd')),
                         dict(headers=[(1970628964, 'ToEnd', 0, 8, len(nested), 8 + len(nested))]))

    def test_count_and_input_limits(self):
        self.assertEqual(qualification.inspect(b'\0' * (qualification.MAX_INPUT + 1)), dict(error='Limit'))
        empty = qualification.envelope(b'test', b'')
        self.assertEqual(len(qualification.inspect(empty * qualification.MAX_BOXES)['headers']), qualification.MAX_BOXES)
        self.assertEqual(qualification.inspect(empty * (qualification.MAX_BOXES + 1)), dict(error='Limit'))

    def test_every_partial_header(self):
        value = qualification.envelope(b'test', b'', 'Extended')
        for cut in range(1, len(value)):
            with self.subTest(cut=cut):
                self.assertEqual(qualification.inspect(value[:cut]), dict(error='Truncated'))

    def test_unknown_form_fails(self):
        with self.assertRaises(ValueError):
            qualification.envelope(b'test', b'', 'unknown')

    def test_empty_sequence(self):
        qualification.check_worker('0 0 ok 0 \n', self.heap, [dict(data=b'')], [0])

    def test_exact_error(self):
        qualification.check_worker('0 0 error Truncated\n', self.heap, [dict(data=b'\0')], [0])

    def test_wrong_error_fails(self):
        with self.assertRaisesRegex(ValueError, 'framing error'):
            qualification.check_worker('0 0 error Length\n', self.heap, [dict(data=b'\0')], [0])

    def test_missing_or_extra_results_fail(self):
        for output in ['', '0 1 ok 0 \n0 1 ok 0 \n']:
            with self.subTest(output=output), self.assertRaisesRegex(ValueError, 'results or heap'):
                self.check(output)

    def test_missing_or_bad_heap_fails(self):
        for heap in ['', 'leakcheck: allocs=0 frees=0 live_bytes=0',
                     'leakcheck: allocs=7 frees=6 live_bytes=0',
                     'leakcheck: allocs=7 frees=7 live_bytes=1']:
            with self.subTest(heap=heap), self.assertRaisesRegex(ValueError, 'results or heap'):
                self.check(heap=heap)

    def test_index_or_signature_mismatch_fails(self):
        for output in ['1 1 ok 0 \n', '0 3 ok 0 \n']:
            with self.subTest(output=output), self.assertRaisesRegex(ValueError, 'signature or index'):
                self.check(output)

    def test_descriptor_mismatch_fails(self):
        for output in ['0 1 ok 0 \n', '0 1 ok 1 1952805748:Short:0:8:2:10,\n',
                       '0 1 ok 1 1952805748:Extended:0:8:3:11,\n', '0 1 error Length\n']:
            with self.subTest(output=output), self.assertRaisesRegex(ValueError, 'box descriptor'):
                self.check(output)

    def test_signature_count_mismatch_fails(self):
        with self.assertRaises(ValueError):
            qualification.check_worker('0 0 ok 0 \n', self.heap, [dict(data=b'')], [])


if __name__ == '__main__':
    unittest.main()
