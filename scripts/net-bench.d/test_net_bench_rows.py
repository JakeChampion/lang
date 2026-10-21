"""Cover net_bench_rows.py — the row parsing and the CPU denominator.

The denominator tests are the ones a hand-check against a healthy sample
cannot stand in for: when nothing fails, h2load's `done` and `succeeded`
are equal and oha's handled count equals its 200s, so only a load with
failures tells the right denominator from the wrong one.
"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "net_bench_rows.py")
TICK_US = 1e6 / os.sysconf("SC_CLK_TCK")


def run(*args):
    out = subprocess.run(
        [sys.executable, SCRIPT, *args],
        capture_output=True, text=True, check=True,
    ).stdout
    return dict(line.split("\t") for line in out.splitlines())


class H2loadRowsTest(unittest.TestCase):
    # done=900 differs from succeeded=800, and failed+errored+timeout=100.
    SUMMARY = (
        "finished in 10.00s, 90.00 req/s, 1.00MB/s\n"
        "requests: 1000 total, 990 started, 900 done, "
        "800 succeeded, 50 failed, 30 errored, 20 timeout\n"
    )

    def rows(self, u1, s1):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "h3.out")
            log = os.path.join(d, "h3.log")
            pathlib.Path(out).write_text(self.SUMMARY)
            pathlib.Path(log).write_text("0 0 100\n0 0 300\n0 0 200\nnoise\n")
            return run("h2load", "fern-h3", "1", "32", out, log,
                       "0", "0", str(u1), str(s1))

    def test_cpu_divides_by_done_not_succeeded(self):
        r = self.rows(90, 45)
        self.assertAlmostEqual(
            float(r["net/fern-h3/c1m32.server_user_us_per_req"]),
            90 * TICK_US / 900, places=2)
        self.assertAlmostEqual(
            float(r["net/fern-h3/c1m32.server_sys_us_per_req"]),
            45 * TICK_US / 900, places=2)

    def test_failed_is_failed_plus_errored_plus_timeout(self):
        self.assertEqual(self.rows(0, 0)["net/fern-h3/c1m32.failed"], "100")

    def test_rps_and_percentiles(self):
        r = self.rows(0, 0)
        self.assertEqual(r["net/fern-h3/c1m32.rps"], "90")
        self.assertEqual(r["net/fern-h3/c1m32.p50_us"], "200")
        self.assertEqual(r["net/fern-h3/c1m32.p99_us"], "300")


class OhaRowsTest(unittest.TestCase):
    SAMPLE = {
        "summary": {"requestsPerSec": 123.9},
        "latencyPercentiles": {"p50": 0.0012, "p99": 0.0099},
        "statusCodeDistribution": {"200": 900, "500": 50},
        "errorDistribution": {"connection error": 30, "deadline has elapsed": 20},
    }

    def rows(self, u1, s1):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "o.json")
            pathlib.Path(path).write_text(json.dumps(self.SAMPLE))
            return run("oha", "fern", "64", path, "0", "0", str(u1), str(s1))

    def test_cpu_divides_by_codes_plus_errors(self):
        # handled = 900 + 50 codes + 30 + 20 errors = 1000.
        self.assertAlmostEqual(
            float(self.rows(100, 0)["net/fern/c64.server_user_us_per_req"]),
            100 * TICK_US / 1000, places=2)

    def test_failed_counts_non_200_and_non_deadline_errors(self):
        # 50 non-200 + 30 connection errors; the 20 deadline cutoffs do not count.
        self.assertEqual(self.rows(0, 0)["net/fern/c64.failed"], "80")

    def test_rps_and_percentiles(self):
        r = self.rows(0, 0)
        self.assertEqual(r["net/fern/c64.rps"], "123")
        self.assertEqual(r["net/fern/c64.p50_us"], "1200")
        self.assertEqual(r["net/fern/c64.p99_us"], "9900")


class NoRequestsTest(unittest.TestCase):
    def test_zero_handled_emits_no_cpu_rows(self):
        sample = {"summary": {"requestsPerSec": 0},
                  "latencyPercentiles": {"p50": 0, "p99": 0}}
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "o.json")
            pathlib.Path(path).write_text(json.dumps(sample))
            r = run("oha", "fern", "64", path, "0", "0", "5", "5")
        self.assertNotIn("net/fern/c64.server_user_us_per_req", r)
        self.assertEqual(r["net/fern/c64.failed"], "0")


if __name__ == "__main__":
    unittest.main()
