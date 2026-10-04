"""Exercise the log table generator: its region splicing, refusal paths, and
the properties of the data it computes."""
import math
import struct
import unittest

import gen_log_table as g


class LogTableGenerationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.data = g.generate()

    def render(self, indent):
        return g.render_asm_region(self.data, indent)

    def test_regeneration_preserves_surrounding_bytes(self):
        text = "  s = prefix;\r\n  " + g.BEGIN + "\n  stale\n  " + g.END + "\r\n  s = suffix;"
        want = "  s = prefix;\r\n" + self.render("  ") + "\r\n  s = suffix;"
        self.assertEqual(g.regenerated(text, self.render), want)

    def test_regeneration_is_idempotent(self):
        once = g.regenerated("x\n" + g.BEGIN + "\n" + g.END + "\ny", self.render)
        self.assertEqual(g.regenerated(once, self.render), once)

    def test_invalid_markers_are_refused(self):
        for text in ["", g.BEGIN, g.END + "\n" + g.BEGIN, (g.BEGIN + "\n" + g.END + "\n") * 2,
                     "code " + g.BEGIN + "\n" + g.END]:
            with self.subTest(text=text[:60]):
                self.assertIsNone(g.regenerated(text, self.render))

    def test_logc_hi_is_on_the_grid_and_rows_are_ordered(self):
        # k*ln2hi + logc_hi is exact only if logc_hi carries no bit below 2^-43.
        for invc, hi, lo in self.data["rows"]:
            self.assertEqual(hi * 2.0**g.LOGC_GRID, math.floor(hi * 2.0**g.LOGC_GRID))
            self.assertLess(abs(lo), 2.0**-g.LOGC_GRID)
        # c rises with the row index, across z = 1 too, so invc falls.
        invcs = [row[0] for row in self.data["rows"]]
        self.assertEqual(len(invcs), g.N)
        self.assertTrue(all(b < a for a, b in zip(invcs, invcs[1:])))

    def test_polynomials_meet_their_budgets(self):
        # The main path's absolute error has to stay far below the 2^-56 ulp of
        # its smallest result; the near-1 path's relative error far below 2^-53.
        self.assertLess(self.data["main_err"], g.D(2) ** -62)
        self.assertLess(self.data["near_err"], g.D(2) ** -60)

    def test_wasm_payload_is_the_rows_little_endian(self):
        region = g.render_wasm_region(self.data, "")
        first = self.data["rows"][0]
        want = "".join("\\\\%02x" % b for v in first for b in struct.pack("<d", v))
        self.assertIn('"' + want + '",', region)


if __name__ == "__main__":
    unittest.main()
