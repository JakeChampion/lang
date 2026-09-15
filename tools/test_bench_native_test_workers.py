import json
from pathlib import Path
import tempfile
import unittest

from bench_native_test_workers import collect_outcomes, verify_events


class OutcomeChecks(unittest.TestCase):
    def read(self, rows):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path / "summary.json").write_text(json.dumps(rows))
            return collect_outcomes(path)

    def test_complete_disjoint_workers(self):
        parents, outcomes = self.read([
            {"tests": ["TestA"], "outcomes": {"TestA": "pass", "TestA/sub": "skip"}},
            {"tests": ["TestB"], "outcomes": {"TestB": "pass"}},
        ])
        self.assertEqual(parents, {"TestA", "TestB"})
        self.assertEqual(outcomes, {"TestA": "pass", "TestA/sub": "skip", "TestB": "pass"})

    def test_invalid_measurements_fail(self):
        good = {"tests": ["TestA"], "outcomes": {"TestA": "pass"}}
        cases = {
            "empty": [],
            "duplicate parent": [good, good],
            "missing parent": [{"tests": ["TestA"], "outcomes": {"TestA/sub": "pass"}}],
            "failure": [{"tests": ["TestA"], "outcomes": {"TestA": "fail"}}],
            "worker error": [{"tests": ["TestA"], "outcomes": {"TestA": "pass"}, "error": "crashed"}],
            "unassigned parent": [{"tests": ["TestA"], "outcomes": {"TestA": "pass", "TestB": "pass"}}],
        }
        for name, rows in cases.items():
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                self.read(rows)


class BaselineChecks(unittest.TestCase):
    def read(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "baseline.jsonl"
            path.write_text("".join(json.dumps(event) + "\n" for event in events))
            return verify_events(path, {"TestA"})

    def test_complete_baseline(self):
        parents, outcomes = self.read([
            {"Action": "run", "Test": "TestA"},
            {"Action": "run", "Test": "TestA/sub"},
            {"Action": "skip", "Test": "TestA/sub"},
            {"Action": "pass", "Test": "TestA"},
            {"Action": "pass"},
        ])
        self.assertEqual(parents, {"TestA"})
        self.assertEqual(outcomes, {"TestA": "pass", "TestA/sub": "skip"})

    def test_incomplete_or_failed_baselines_are_rejected(self):
        run = {"Action": "run", "Test": "TestA"}
        passed = {"Action": "pass", "Test": "TestA"}
        package = {"Action": "pass"}
        cases = {
            "empty": [],
            "missing package": [run, passed],
            "missing test": [package],
            "unfinished child": [run, {"Action": "run", "Test": "TestA/sub"}, passed, package],
            "duplicate start": [run, run, passed, package],
            "duplicate terminal": [run, passed, passed, package],
            "duplicate package": [run, passed, package, package],
            "test failure": [run, {"Action": "fail", "Test": "TestA"}],
            "package failure": [{"Action": "fail"}],
            "unexpected test": [{"Action": "run", "Test": "TestB"}],
        }
        for name, events in cases.items():
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                self.read(events)


if __name__ == "__main__":
    unittest.main()
