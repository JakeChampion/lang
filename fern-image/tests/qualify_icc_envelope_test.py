"""Protocol faults must preserve strict metadata, process, and heap requirements."""

import copy
import hashlib
import unittest

import qualify_icc_envelope as collector


class IccProtocolTests(unittest.TestCase):
    def setUp(self):
        self.case = dict(name="empty", payload_hex="00", payload_sha256=hashlib.sha256(b"\0").hexdigest(),
                         max_bytes=1048576, max_tags=128, cancel_at=0,
                         expected=dict(status="ok", polls=3, fields=[132, 1, 0, 0, 0, 0, 2026, 10, 27, 2, 0, 0, 0, 0] + [0] * 16),
                         offline=dict(applicable=False, observation=None))
        self.cancelled = copy.deepcopy(self.case)
        self.cancelled.update(name="cancelled", cancel_at=1, expected=dict(status="error", error="Cancelled", polls=1))
        self.cases = [self.case, self.cancelled]
        self.names = ["case-0.icc-worker", "case-1.icc-worker"]
        self.stdout = "\n".join(collector.protocol_line(n, c["expected"]) for c, n in zip(self.cases, self.names, strict=True)) + "\n"
        self.stderr = "leakcheck: allocs=19 frees=19 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, cases=None, names=None, code=0):
        return collector.check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr,
                                      self.cases if cases is None else cases, self.names if names is None else names, code)

    def test_success_and_error(self):
        samples, census = self.check()
        self.assertEqual(samples[0]["observation"]["fields"], self.case["expected"]["fields"])
        self.assertEqual(samples[1]["observation"]["error"], "Cancelled")
        self.assertEqual(census, dict(allocs=19, frees=19, live_bytes=0))

    def test_every_metadata_field(self):
        for i in range(30):
            expected = copy.deepcopy(self.case["expected"])
            expected["fields"][i] += 1
            text = collector.protocol_line(self.names[0], expected) + "\n" + self.stdout.splitlines()[1] + "\n"
            with self.assertRaises(ValueError):
                self.check(stdout=text)

    def test_errors_and_polls(self):
        for text in ["error Layout 1", "error Cancelled 2", "error Unknown 1"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("error Cancelled 1", text))

    def test_missing_extra_reordered_and_duplicate_rows(self):
        lines = self.stdout.splitlines()
        for text in ["", lines[0] + "\n", self.stdout + "extra\n", "\n".join(reversed(lines)) + "\n", self.stdout + "\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=text)
        for names in [self.names[::-1], self.names[:1], [self.names[0]] * 2]:
            with self.assertRaises(ValueError):
                self.check(names=names)

    def test_heap_requirements(self):
        for text in ["", self.stderr * 2, "leakcheck: allocs=19 frees=18 live_bytes=0", "leakcheck: allocs=19 frees=19 live_bytes=1", "leakcheck: allocs=0 frees=0 live_bytes=0"]:
            with self.assertRaises(ValueError):
                self.check(stderr=text)

    def test_nonzero_exit(self):
        for code in [1, 3, 137, -9]:
            with self.assertRaises(ValueError):
                self.check(code=code)

    def test_sanitizer_finding(self):
        with self.assertRaises(ValueError):
            self.check(stderr=self.stderr + "fern-sanitizer: leak 0 bytes in 0 blocks\n")

    def test_reference_identity_and_shape(self):
        for change in [dict(payload_hex="01"), dict(max_tags=True), dict(cancel_at=-1),
                       dict(expected=dict(self.case["expected"], fields=[0])),
                       dict(expected=dict(status="error", error="Unknown", polls=0))]:
            changed = copy.deepcopy(self.case)
            changed.update(change)
            with self.assertRaises(ValueError):
                self.check(cases=[changed, self.cancelled])


if __name__ == "__main__":
    unittest.main()
