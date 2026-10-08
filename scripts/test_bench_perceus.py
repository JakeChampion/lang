"""Failure records must survive both ordinary timeouts and an unreapable child."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("bench_perceus", Path(__file__).with_name("bench-perceus.py"))
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)


class MeasurementTests(unittest.TestCase):
    @unittest.skipUnless(sys.platform.startswith("linux"), "explicit Linux resource policy")
    def test_unlimited_stack_reaches_child(self):
        with tempfile.TemporaryDirectory() as directory:
            prefix = Path(directory) / "limits"
            command = [sys.executable, "-c", "import resource; print(resource.getrlimit(resource.RLIMIT_STACK)[0] == resource.RLIM_INFINITY)"]
            bench.measure(command, prefix, 2, unlimited_stack=True)
            self.assertEqual(prefix.with_suffix(".stdout").read_text(), "True\n")

    def test_success_and_failure_preserve_output(self):
        for code in (0, 7):
            with self.subTest(code=code), tempfile.TemporaryDirectory() as directory:
                prefix = Path(directory) / "run"
                command = [sys.executable, "-c", f"print('answer'); raise SystemExit({code})"]
                if code:
                    with self.assertRaises(RuntimeError):
                        bench.measure(command, prefix, 2)
                else:
                    bench.measure(command, prefix, 2)
                record = json.loads(prefix.with_suffix(".json").read_text())
                self.assertEqual(record["exit"], code)
                self.assertEqual(prefix.with_suffix(".stdout").read_text(), "answer\n")
                self.assertGreater(record["wall_ns"], 0)

    def test_timeout_records_actual_reaped_status(self):
        with tempfile.TemporaryDirectory() as directory:
            prefix = Path(directory) / "timeout"
            with self.assertRaises(TimeoutError):
                bench.measure([sys.executable, "-c", "import time; print('started', flush=True); time.sleep(30)"], prefix, 1)
            record = json.loads(prefix.with_suffix(".json").read_text())
            self.assertTrue(record["timed_out"])
            self.assertTrue(record["reaped"])
            self.assertEqual(record["exit"], -9)
            self.assertEqual(prefix.with_suffix(".stdout").read_text(), "started\n")

    def test_unreaped_child_has_no_fabricated_exit_or_metrics(self):
        # A fake process models the kernel refusing to finish exit. No real
        # unkillable child is created by this regression.
        with tempfile.TemporaryDirectory() as directory:
            prefix = Path(directory) / "blocked"
            process = unittest.mock.Mock(spec=subprocess.Popen)
            process.pid = 123
            process.returncode = None

            def wait(pid, flags):
                if flags == 0:
                    raise TimeoutError("workload deadline")
                return 0, 0, None

            with patch.object(bench.subprocess, "Popen", return_value=process), \
                    patch.object(bench.os, "wait4", side_effect=wait), \
                    patch.object(bench.time, "monotonic", side_effect=[0, 0, 3]), \
                    patch.object(bench.time, "sleep"):
                with self.assertRaises(TimeoutError):
                    bench.measure(["blocked"], prefix, 1)
            record = json.loads(prefix.with_suffix(".json").read_text())
            process.kill.assert_called_once()
            self.assertFalse(record["reaped"])
            self.assertIsNone(record["exit"])
            self.assertIsNone(process.returncode)
            self.assertNotIn("peak_rss_bytes", record)
            self.assertNotIn("wall_ns", record)
            self.assertEqual(record["pid"], 123)


if __name__ == "__main__":
    unittest.main()
