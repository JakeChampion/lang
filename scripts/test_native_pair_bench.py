import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class NativePairBenchTests(unittest.TestCase):
    def test_compiler_runs_use_separate_cache_policies(self):
        with tempfile.TemporaryDirectory(prefix="native pair test ") as temporary:
            root = Path(temporary)
            log = root / "calls.jsonl"
            compiler = '''#!/usr/bin/env python3
import json, os, pathlib, sys
with open(os.environ["BENCH_TEST_LOG"], "a") as f:
    f.write(json.dumps({"compiler": pathlib.Path(sys.argv[0]).name,
                       "source": sys.argv[-2],
                       "certify": os.environ.get("FERN_RC_CERTIFY"),
                       "memo": os.environ.get("FERN_RC_CERTIFY_MEMO")}) + "\\n")
output = pathlib.Path(sys.argv[sys.argv.index("-o") + 1])
output.write_text("#!/usr/bin/env python3\\nprint('ok')\\n")
output.chmod(0o755)
'''
            for name in ["baseline", "candidate"]:
                path = root / name
                path.write_text(compiler)
                path.chmod(0o755)
            report = root / "report.json"
            environment = os.environ.copy()
            environment.update(BENCH_TEST_LOG=str(log), FERN_RC_CERTIFY="0",
                               FERN_RC_CERTIFY_MEMO="inherited-cache")
            script = Path(__file__).with_name("native-pair-bench")
            command = [sys.executable, str(script), str(root / "baseline"),
                       str(root / "candidate"), str(root), str(report),
                       "--compiler-pairs", "1"]
            result = subprocess.run(command, env=environment, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            data = json.loads(report.read_text())
            self.assertEqual(data["compiler_cold"]["proof_cache"], "disabled")
            self.assertEqual(data["compiler"]["proof_cache"], "isolated-warm")
            rows = [json.loads(line) for line in log.read_text().splitlines()]
            rows = [row for row in rows if row["source"].endswith("compiler/checker.fern")]
            self.assertEqual(len(rows), 8)
            self.assertTrue(all(row["certify"] == "1" for row in rows))
            self.assertTrue(all(row["memo"] == "0" for row in rows[:4]))
            caches = {row["compiler"]: row["memo"] for row in rows[4:]}
            self.assertNotEqual(caches["baseline"], caches["candidate"])
            for name, cache in caches.items():
                self.assertEqual(Path(cache).parts[-2:], ("proof-cache", name))
            self.assertEqual(len(data["compiler_cold"]["paired_ratios"]), 1)
            self.assertEqual(len(data["compiler"]["paired_ratios"]), 1)

    def test_nonpositive_pair_count_is_rejected(self):
        script = Path(__file__).with_name("native-pair-bench")
        result = subprocess.run([sys.executable, str(script), "a", "b", "c", "d",
                                 "--compiler-pairs", "0"], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must be positive", result.stderr)


if __name__ == "__main__":
    unittest.main()
