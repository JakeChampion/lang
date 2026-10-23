"""Cover deflate_bench_rows.py: the nearest-level choice and the medians."""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import deflate_bench_rows as rows  # noqa: E402


def row(impl, level, name, out, comp, decomp, cross):
    return {"impl": impl, "level": level, "file": name, "in": "1000", "out": str(out),
            "comp_ns": str(comp), "decomp_ns": str(decomp), "cross_decomp_ns": str(cross)}


class Summarise(unittest.TestCase):
    def test_compares_with_the_level_of_nearest_size(self):
        data = [row("fern", "-", "a", 300, 800, 400, 200)]
        data += [row("libdeflate", str(l), "a", 400 - 20 * l, 100, 50, 300) for l in range(1, 13)]
        per_file, _ = rows.summarise(data)
        self.assertEqual(per_file[0]["near_level"], 5)
        self.assertAlmostEqual(per_file[0]["comp_slowdown"], 8.0)
        self.assertAlmostEqual(per_file[0]["decomp_slowdown"], 8.0)

    def test_same_stream_uses_level_six(self):
        data = [row("fern", "-", "a", 300, 800, 400, 200)]
        data += [row("libdeflate", str(l), "a", 300, 100, 50 if l == 6 else 10, 300) for l in range(1, 13)]
        _, medians = rows.summarise(data)
        self.assertAlmostEqual(medians["same_stream_slowdown"], 6.0)

    def test_medians_are_over_files(self):
        data = []
        for name, comp in (("a", 200), ("b", 400), ("c", 900)):
            data.append(row("fern", "-", name, 300, comp, 100, 100))
            data += [row("libdeflate", str(l), name, 300, 100, 100, 100) for l in range(1, 13)]
        _, medians = rows.summarise(data)
        self.assertAlmostEqual(medians["comp_slowdown"], 4.0)


if __name__ == "__main__":
    unittest.main()
