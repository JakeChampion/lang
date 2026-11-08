import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('silk_tables', Path(__file__).with_name('gen-silk-tables.py'))
tables = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tables)

class Tables(unittest.TestCase):
    def test_zero_probability_symbol_keeps_its_index(self):
        self.assertEqual(tables.inverse_cdf([256, 0, 96, 160, 256]), ([160, 96, 0], 1))

    def test_ragged_rows_have_separate_offsets(self):
        source = 'var x = [][]int8{{-1, 2}, {3}}'
        result = tables.generate(tables.extract(source))
        self.assertIn('-1, 2, 3,', result)
        self.assertIn('0, 2, 3,', result)

    def test_invalid_probabilities_are_rejected(self):
        for row in [[256, 100, 99, 256], [128, 20, 128], [256, -1, 256], [256, 1, 255]]:
            with self.assertRaises(ValueError):
                tables.inverse_cdf(row)

    def test_fixed_array_cannot_lose_implicit_padding(self):
        with self.assertRaises(ValueError):
            tables.extract('var x = [4]int32{1, 2}')

if __name__ == '__main__':
    unittest.main()
