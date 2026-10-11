"""Exercise the pow table generator: its region splicing and the properties
the kernel's exactness arguments rest on."""
import math
import unittest

import gen_pow_table as g


class PowTableGenerationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data = g.generate()

    def render(self, indent):
        return g.render_fern_region(self.data, indent)

    def test_regeneration_is_idempotent_and_leaves_the_log_region_alone(self):
        text = "x\n" + g.base.BEGIN + "\nlog\n" + g.base.END + "\n" + g.BEGIN + "\n" + g.END + "\ny"
        once = g.base.regenerated(text, self.render, g.BEGIN, g.END)
        self.assertIn(g.base.BEGIN + "\nlog\n" + g.base.END, once)
        self.assertEqual(g.base.regenerated(once, self.render, g.BEGIN, g.END), once)
        # Neither region's markers are mistaken for the other's.
        self.assertNotIn(g.base.END, g.END)
        self.assertNotIn(g.END, g.base.END)

    def test_invc_is_short_and_rows_around_one_are_exact(self):
        # z * invc - 1 is exact only while invc has few significant bits; c = 1
        # rows make log(c) vanish where x is near 1.
        for invc, logc, tail in self.data["log_rows"]:
            self.assertEqual((invc * 2 * g.N) % 1, 0)
            self.assertEqual(logc * 2.0**g.LOGC_GRID, math.floor(logc * 2.0**g.LOGC_GRID))
            self.assertLess(abs(tail), 2.0**-g.LOGC_GRID)
            if invc == 1.0:
                self.assertEqual((logc, tail), (0.0, 0.0))
        row1 = (0x3FF0000000000000 - g.LOG_OFF) >> (52 - g.TABLE_BITS)
        self.assertEqual(self.data["log_rows"][row1][0], 1.0)
        self.assertLess(self.data["rmax"], 1.0 / g.N)

    def test_split_constants_make_their_products_exact(self):
        # k * ln2hi (|k| < 2^11) and k * ln2hi/N (|k| < 2^18) fit 53 bits.
        self.assertLessEqual(sig_bits(self.data["ln2hi"]) + 11, 53)
        self.assertLessEqual(sig_bits(self.data["ln2n_hi"]) + 18, 53)

    def test_exp_table_rows(self):
        rows = self.data["exp_rows"]
        self.assertEqual(rows[0], (1.0, 0.0))
        self.assertTrue(all(b[0] > a[0] for a, b in zip(rows, rows[1:])))
        self.assertTrue(all(abs(t) < 2.0**-53 for _, t in rows))

    def test_polynomials_meet_their_budgets(self):
        self.assertLess(self.data["log_err"], g.D(2) ** -66)
        self.assertLess(self.data["exp_err"], g.D(2) ** -63)

    def test_literals_read_back_exactly(self):
        region = self.render("")
        self.assertTrue(region.startswith(g.BEGIN) and region.endswith(g.END))
        vals = [v for row in self.data["log_rows"] for v in row] + [v for row in self.data["exp_rows"] for v in row]
        for v in vals + self.data["log_poly"] + self.data["exp_poly"]:
            self.assertEqual(float(g.literal(v)), v)


def sig_bits(x):
    m, _ = math.frexp(x)
    n = int(abs(m) * 2.0**53)
    return 53 - ((n & -n).bit_length() - 1)


if __name__ == "__main__":
    unittest.main()
