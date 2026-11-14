import os
import subprocess
import tempfile
import unittest

REBANK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "rebank")


def run(text, rows, *flags):
    with tempfile.NamedTemporaryFile("w", suffix=".txt", delete=False) as f:
        f.write(text)
    try:
        r = subprocess.run([REBANK, *flags, f.name], input=rows, capture_output=True, text=True)
        with open(f.name) as g:
            return r, g.read()
    finally:
        os.unlink(f.name)


class RebankTest(unittest.TestCase):
    def test_moves_a_row_and_keeps_its_place_separator_and_tolerance(self):
        r, out = run("# header 12\na/x.ir\t100\na/y.ir\t200 2\n", "    a/y.ir 210\n")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(out, "# header 12\na/x.ir\t100\na/y.ir\t210 2\n")

    def test_an_input_tolerance_replaces_the_tolerance_of_the_row(self):
        _, out = run("a/y.ir\t200 2\n", "a/y.ir 210 5\n")
        self.assertEqual(out, "a/y.ir\t210 5\n")

    def test_a_new_metric_goes_at_the_end_with_the_separator_of_the_file(self):
        _, out = run("drivers/x.fern 5\n", "drivers/y.fern 3\n")
        self.assertEqual(out, "drivers/x.fern 5\ndrivers/y.fern 3\n")

    def test_a_line_that_is_not_a_row_changes_nothing(self):
        r, out = run("a/x.ir\t100\n", "a/x.ir 110\nnot a row at all\n")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(out, "a/x.ir\t100\n")

    def test_two_rows_for_one_metric_is_an_error(self):
        r, out = run("a/x.ir\t100\na/x.ir\t100\n", "a/x.ir 110\n")
        self.assertEqual(r.returncode, 1)
        self.assertEqual(out, "a/x.ir\t100\na/x.ir\t100\n")

    def test_a_comment_that_looks_like_a_row_stays(self):
        _, out = run("# a/x.ir 1\na/x.ir\t100\n", "a/x.ir 110\n")
        self.assertEqual(out, "# a/x.ir 1\na/x.ir\t110\n")

    def test_max_drift_holds_a_large_move_and_makes_a_small_one(self):
        r, out = run("a/x.ir\t1000\na/y.ir\t1000\na/z.ir\t1000\n", "a/x.ir 1050\na/y.ir 1051\na/z.ir 900\n", "--max-drift", "5")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(out, "a/x.ir\t1050\na/y.ir\t1000\na/z.ir\t1000\n")
        self.assertIn("held a/y.ir 1000 -> 1051", r.stdout)
        self.assertIn("held a/z.ir 1000 -> 900", r.stdout)


if __name__ == "__main__":
    unittest.main()
