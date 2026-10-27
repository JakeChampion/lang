"""Protocol faults must preserve numerical, process, reference, and heap requirements."""

import copy
import struct
import unittest

import qualify_icc_affine_inverse as collector


def bits(value):
    return struct.unpack("<q", struct.pack("<d", value))[0]


class IccAffineInverseProtocolTests(unittest.TestCase):
    def setUp(self):
        self.case = dict(name="half", function_type=1, input_bits=bits(0.25), coefficients=[131072,65536,0], cancel_at=0, expected=dict(status="ok", polls=2, fraction=["1", "2"]))
        self.case["payload_sha256"] = collector.sha(collector.packet(self.case))
        self.cancelled = copy.deepcopy(self.case)
        self.cancelled.update(name="cancelled", cancel_at=1, expected=dict(status="error", error="Cancelled", polls=1))
        self.cancelled["payload_sha256"] = collector.sha(collector.packet(self.cancelled))
        self.cases = [self.case, self.cancelled]
        self.names = ["case-0.icc-worker", "case-1.icc-worker"]
        self.stdout = f"aiv {self.names[0]} ok 2 {bits(0.5)}\naiv {self.names[1]} error Cancelled 1\n"
        self.stderr = "leakcheck: allocs=19 frees=19 live_bytes=0\n"

    def check(self, stdout=None, stderr=None, cases=None, names=None, code=0):
        return collector.check_output(self.stdout if stdout is None else stdout, self.stderr if stderr is None else stderr,
                                      self.cases if cases is None else cases, self.names if names is None else names, code)

    def test_success_and_error(self):
        samples, census = self.check()
        self.assertEqual(samples[0]["observation"]["result_bits"], bits(0.5))
        self.assertEqual(samples[1]["observation"]["error"], "Cancelled")
        self.assertEqual(census, dict(allocs=19, frees=19, live_bytes=0))

    def test_finite_criterion_and_nonfinite_results(self):
        for value in [0.5 + 2e-12, 0.5 - 2e-12, float("inf"), float("nan"), -0.01, 1.01]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace(str(bits(0.5)), str(bits(value))))

    def test_exact_bits(self):
        cases = copy.deepcopy(self.cases)
        cases[0]["expected"]["exact_bits"] = bits(0.5)
        with self.assertRaises(ValueError):
            self.check(cases=cases, stdout=self.stdout.replace(str(bits(0.5)), str(bits(0.5) + 1)))

    def test_errors_and_polls(self):
        for text in ["error Domain 1", "error Cancelled 2", "error Unknown 1"]:
            with self.assertRaises(ValueError):
                self.check(stdout=self.stdout.replace("error Cancelled 1", text))
        with self.assertRaises(ValueError):
            self.check(stdout=self.stdout.replace("ok 2", "ok 3"))

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

    def test_unsupported_admission_before_callbacks(self):
        case=copy.deepcopy(self.case)
        case.update(function_type=3,cancel_at=1,expected=dict(status="error",error="Unsupported",polls=0))
        case['payload_sha256']=collector.sha(collector.packet(case))
        self.check(cases=[case],names=[self.names[0]],stdout=f"aiv {self.names[0]} error Unsupported 0\n")
        with self.assertRaises(ValueError):
            self.check(cases=[case],names=[self.names[0]],stdout=f"aiv {self.names[0]} error Cancelled 1\n")

    def test_flat_and_range_policies(self):
        for coefficients,x,q,wrong in [
            ([65536,65536,0,32768],1.0,0.5,1.0),
            ([65536,131072,-65536,32768],0.5,0.5,0.0),
            ([262144,16384,-16383,32768],float.fromhex('0x1.0000000000001p-1'),1.0,0.99993896484375),
        ]:
            case=copy.deepcopy(self.case)
            case.update(function_type=2,coefficients=coefficients,input_bits=bits(x),expected=dict(status="ok",polls=2,decimal=str(q),exact_bits=bits(q)))
            case['payload_sha256']=collector.sha(collector.packet(case))
            self.check(cases=[case],names=[self.names[0]],stdout=f"aiv {self.names[0]} ok 2 {bits(q)}\n")
            with self.assertRaises(ValueError):
                self.check(cases=[case],names=[self.names[0]],stdout=f"aiv {self.names[0]} ok 2 {bits(wrong)}\n")

    def test_reference_identity_and_shape(self):
        for change in [dict(input_bits=bits(0.75)), dict(function_type=True), dict(cancel_at=-1),
                       dict(expected=dict(self.case["expected"], fraction=["1", "0"])),
                       dict(expected=dict(status="error", error="Unknown", polls=0)),
                       dict(expected=dict(status="error", error="Numeric", polls=0))]:
            changed = copy.deepcopy(self.case)
            changed.update(change)
            with self.assertRaises((ValueError, ZeroDivisionError)):
                self.check(cases=[changed, self.cancelled])


if __name__ == "__main__":
    unittest.main()
