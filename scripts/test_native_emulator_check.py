import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location(
    "native_emulator_check",
    Path(__file__).resolve().parents[1] / "examples/android/native/emulator_check.py",
)
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)


class InstalledSizeTest(unittest.TestCase):
    def measure(self, extracted):
        package = "dev.fern.nativez" if extracted else "dev.fern.native"
        base = f"/data/app/{package}/base.apk"
        payload = b"download fixture"

        def adb(*args):
            if args == ("shell", "stat", "-c", "%s", base):
                return str(len(payload)) + "\r\n"
            if extracted and args == ("shell", "stat", "-c", "%s", base.rsplit("/", 1)[0] + "/lib/x86_64/libapp.so"):
                return "8193\n"
            if args == ("shell", "du", "-sk", base.rsplit("/", 1)[0]):
                return "20\t" + base.rsplit("/", 1)[0] + "\r\n"
            self.fail(f"unexpected adb command: {args!r}")

        with tempfile.TemporaryDirectory() as directory:
            apk = Path(directory, "app.apk")
            apk.write_bytes(payload)
            with mock.patch.object(native, "adb", side_effect=adb):
                result = native.installed_size(apk, package, base, "extracted" if extracted else "mapped", extracted)
        self.assertEqual(result["download_bytes"], len(payload))
        self.assertEqual(result["device_apk_bytes"], len(payload))
        self.assertEqual(result["install_directory_allocated_bytes"], 20 * 1024)
        return result

    def test_mapped_library_uses_only_the_apk_on_the_device(self):
        self.assertEqual(self.measure(False)["extracted_library_bytes"], 0)

    def test_extracted_library_and_allocated_storage_stay_separate(self):
        result = self.measure(True)
        self.assertEqual(result["extracted_library_bytes"], 8193)
        self.assertNotEqual(result["install_directory_allocated_bytes"], result["device_apk_bytes"] + result["extracted_library_bytes"])

    def test_a_failed_device_measurement_does_not_become_zero(self):
        error = subprocess.CalledProcessError(1, ["adb", "shell", "stat"])
        with mock.patch.object(native, "adb", side_effect=error):
            with self.assertRaises(subprocess.CalledProcessError):
                native.installed_size("app.apk", "dev.fern.native", "/data/app/base.apk", "mapped", False)

    def test_invalid_device_output_does_not_become_zero(self):
        with mock.patch.object(native, "adb", return_value="permission denied\n"):
            with self.assertRaises(ValueError):
                native.installed_size("app.apk", "dev.fern.native", "/data/app/base.apk", "mapped", False)


if __name__ == "__main__":
    unittest.main()
