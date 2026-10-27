"""Protocol faults must preserve byte comparisons, exact refusals, and worker cleanup."""

import copy
from types import SimpleNamespace
import unittest

import qualify_icc_app2 as collector


def case(name, status="some", stop=0):
    profile = bytes(range(130))
    expected = dict(status="some", polls=6, length=130, profile_sha256=collector.sha(profile), checked_bytes=130)
    value = dict(name=name, profile=dict(kind="pattern", length=130, pattern_hex=profile.hex()),
                 chunks=[dict(sequence=1, total=1, start=0, length=130)],
                 max_profile_bytes=1048576, max_chunks=32, cancel_at=stop, expected=expected)
    if status == "error":
        value["expected"] = dict(status="error", error="Cancelled", polls=stop)
    elif status == "none":
        value["profile"]["length"] = 0
        value["chunks"] = []
        value["expected"] = dict(status="none", polls=2)
    _, payloads = collector.materialize(value)
    value["payload_sha256"] = [collector.sha(p) for p in payloads]
    value["packet_sha256"] = collector.sha(collector.packet(value))
    return value


class IccApp2ProtocolTests(unittest.TestCase):
    def setUp(self):
        self.cases = [case("present"), case("absent", "none"), case("cancelled", "error", 1)]
        self.names = ["case-0.icc-worker", "case-1.icc-worker", "case-2.icc-worker"]
        self.stdout = "\n".join(collector.protocol_line(n, c["expected"]) for c, n in zip(self.cases, self.names, strict=True)) + "\n"
        self.stderr = "leakcheck: allocs=19 frees=19 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, cases=None, names=None, code=0):
        return collector.check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr,
                                      self.cases if cases is None else cases, self.names if names is None else names, code)

    def test_success_absence_and_error(self):
        samples, census = self.check()
        self.assertEqual([s["observation"]["status"] for s in samples], ["some", "none", "error"])
        self.assertEqual(samples[0]["observation"]["checked_bytes"], 130)
        self.assertEqual(census, dict(allocs=19, frees=19, live_bytes=0))

    def test_every_observed_byte_comparison_field(self):
        for field in ["polls", "length", "checked_bytes"]:
            expected = copy.deepcopy(self.cases[0]["expected"])
            expected[field] += 1
            text = collector.protocol_line(self.names[0], expected) + "\n" + "\n".join(self.stdout.splitlines()[1:]) + "\n"
            with self.assertRaises(ValueError):
                self.check(stdout=text)

    def test_errors_absence_and_polls(self):
        for text in ["error Missing 1", "error Cancelled 2", "error Unknown 1"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("error Cancelled 1", text))
        for text in ["some 2 0 0", "none 3"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("none 2", text))

    def test_missing_extra_reordered_and_duplicate_rows(self):
        lines = self.stdout.splitlines()
        for text in ["", lines[0] + "\n", self.stdout + "extra\n", "\n".join(reversed(lines)) + "\n", self.stdout + "\n"]:
            with self.assertRaises(ValueError):
                self.check(stdout=text)
        for names in [self.names[::-1], self.names[:1], [self.names[0]] * 3]:
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
        for change in [dict(max_chunks=True), dict(cancel_at=-1), dict(packet_sha256="00"),
                       dict(expected=dict(self.cases[0]["expected"], checked_bytes=129)),
                       dict(expected=dict(status="error", error="Unknown", polls=0))]:
            changed = copy.deepcopy(self.cases[0])
            changed.update(change)
            with self.assertRaises(ValueError):
                self.check(cases=[changed, *self.cases[1:]])

    def test_reference_rejection_must_have_the_correct_reason(self):
        good = SimpleNamespace(returncode=3, stdout="app2 control mismatch byte 64 64 65\n", stderr=self.stderr)
        self.assertEqual(collector.check_reference_rejection(good, 3, "control", 64, 64, 65)["live_bytes"], 0)
        for change in [dict(returncode=0), dict(returncode=137), dict(stdout="app2 control mismatch byte 0 0 1\n"),
                       dict(stdout="app2 control error Length 0\n"), dict(stderr=self.stderr + "fern-sanitizer: bad\n")]:
            bad = SimpleNamespace(**dict(vars(good), **change))
            with self.assertRaises(ValueError):
                collector.check_reference_rejection(bad, 3, "control", 64, 64, 65)


if __name__ == "__main__":
    unittest.main()
