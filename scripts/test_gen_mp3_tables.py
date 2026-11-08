import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('mp3_tables', Path(__file__).with_name('gen-mp3-tables.py'))
tables = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tables)


class CInitializers(unittest.TestCase):
    def test_partial_rows_preserve_the_declared_stride(self):
        source = 'static const uint8_t mixed[3][4] = {{1,2}, {3,4,5}, {6}};'
        floating, values = tables.initializer(source, 'mixed')
        self.assertFalse(floating)
        self.assertEqual([int(v) for v in values], [1, 2, 0, 0, 3, 4, 5, 0, 6, 0, 0, 0])

    def test_single_line_array_stops_at_its_own_brace(self):
        source = 'static const float gain[3] = {1.f,-2.5f,3e-4f};\nstatic const uint8_t next[1] = {99};'
        floating, values = tables.initializer(source, 'gain')
        self.assertTrue(floating)
        self.assertEqual(values, ['1.f', '-2.5f', '3e-4f'])

    def test_oversized_rows_fail(self):
        with self.assertRaises(AssertionError):
            tables.initializer('static const uint8_t broken[1][2] = {{1,2,3}};', 'broken')

    def test_comments_do_not_add_table_entries(self):
        _, values = tables.initializer('static const uint8_t tab[2] = {1, /* 99 */ 2};', 'tab')
        self.assertEqual(values, ['1', '2'])
