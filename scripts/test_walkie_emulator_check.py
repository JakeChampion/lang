import contextlib
import importlib.util
import io
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


spec = importlib.util.spec_from_file_location(
    "walkie_emulator_check",
    Path(__file__).resolve().parents[1] / "examples/android/walkie/emulator_check.py",
)
walkie = importlib.util.module_from_spec(spec)
spec.loader.exec_module(walkie)


class FreezerTest(unittest.TestCase):
    def test_waits_for_the_live_state_without_a_reboot(self):
        states = iter(["use_freezer=true\n", "  use_freezer=false\n"])
        commands = []

        def adb(*args, **kwargs):
            commands.append(args)
            if args[:2] == ("shell", "dumpsys"):
                return next(states)
            return ""

        with mock.patch.object(walkie, "adb", side_effect=adb), mock.patch.object(
            walkie.time, "sleep"
        ) as sleep:
            walkie.disable_freezer()

        self.assertEqual(commands[0], ("shell", "settings", "put", "global", "cached_apps_freezer", "disabled"))
        self.assertEqual(commands[1:], [("shell", "dumpsys", "activity", "settings")] * 2)
        sleep.assert_called_once_with(0.5)

    def test_a_written_setting_does_not_replace_the_effective_state(self):
        with mock.patch.object(walkie, "adb", return_value="use_freezer=true\n"), mock.patch.object(
            walkie.time, "monotonic", side_effect=[0, 0, walkie.WAIT_SECONDS + 1]
        ), mock.patch.object(walkie.time, "sleep"):
            with self.assertRaisesRegex(walkie.Failure, "did not disable"):
                walkie.disable_freezer()


class AdbTest(unittest.TestCase):
    def test_an_unresponsive_command_has_a_deadline(self):
        def stalled(command, **kwargs):
            self.assertEqual(command, ["adb", "shell", "pidof", walkie.FIRST])
            self.assertEqual(kwargs["timeout"], walkie.WAIT_SECONDS)
            raise subprocess.TimeoutExpired(command, kwargs["timeout"])

        with mock.patch.object(walkie.subprocess, "run", side_effect=stalled):
            with self.assertRaises(subprocess.TimeoutExpired):
                walkie.pid(walkie.FIRST)

    def test_a_screenshot_requires_a_png(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(
            walkie.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, stdout=b"")
        ) as run:
            with self.assertRaisesRegex(walkie.Failure, "PNG"):
                walkie.screenshot(str(Path(directory, "screen.png")))
            self.assertTrue(run.call_args.kwargs["check"])
            self.assertEqual(run.call_args.kwargs["timeout"], walkie.WAIT_SECONDS)

    def test_screenshot_timeout_is_a_failure(self):
        timeout = subprocess.TimeoutExpired(["adb", "exec-out", "screencap", "-p"], walkie.WAIT_SECONDS)
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(
            walkie.subprocess, "run", side_effect=timeout
        ):
            with self.assertRaises(subprocess.TimeoutExpired):
                walkie.screenshot(str(Path(directory, "screen.png")))


class DiagnosticTest(unittest.TestCase):
    def test_an_unreachable_device_keeps_the_first_failure_and_diagnostics(self):
        timeout = subprocess.TimeoutExpired(["adb", "logcat"], walkie.WAIT_SECONDS)
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(
            walkie, "run", side_effect=walkie.Failure("the peer was lost")
        ), mock.patch.object(walkie, "logcat", side_effect=timeout), mock.patch.object(
            walkie, "adb", side_effect=timeout
        ), contextlib.redirect_stdout(io.StringIO()) as output:
            with self.assertRaises(SystemExit) as result:
                walkie.main("first.apk", "second.apk", directory)
            self.assertEqual(result.exception.code, 1)
            self.assertIn("FAIL the peer was lost", output.getvalue())
            self.assertEqual(Path(directory, "failure.txt").read_text(), "the peer was lost\n")
            self.assertIn("timed out", Path(directory, "logcat.txt").read_text())

    def test_a_timeout_during_the_run_fails_after_cleanup(self):
        timeout = subprocess.TimeoutExpired(["adb", "install"], walkie.WAIT_SECONDS)
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(
            walkie, "run", side_effect=timeout
        ), mock.patch.object(walkie, "logcat", return_value="walkie: diagnostic\n"), mock.patch.object(
            walkie, "adb", return_value=""
        ) as adb, contextlib.redirect_stdout(io.StringIO()):
            with self.assertRaises(SystemExit) as result:
                walkie.main("first.apk", "second.apk", directory)
            self.assertEqual(result.exception.code, 1)
            self.assertEqual(adb.call_count, 2)
            self.assertIn("timed out", Path(directory, "failure.txt").read_text())


if __name__ == "__main__":
    unittest.main()
