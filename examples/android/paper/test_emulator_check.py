"""Check Back navigation with the keyboard and the app drawer."""

import importlib.util
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("paper_check", Path(__file__).with_name("emulator_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class Device:
    def __init__(self, *, stuck_keyboard=False, missing_keyboard=False,
                 ignored_back=False, early_close=False, wrong_home=False,
                 editor="inputType=0x1 imeOptions=0x10000002"):
        self.keyboard = False
        self.editor = editor
        self.layer = 0
        self.drawers = 0
        self.log = []
        self.failures = []
        self.back_events = []
        self.stuck_keyboard = stuck_keyboard
        self.missing_keyboard = missing_keyboard
        self.ignored_back = ignored_back
        self.early_close = early_close
        self.wrong_home = wrong_home

    def adb(self, *args, **kwargs):
        if args == ("logcat", "-c"):
            self.log.clear()
        elif args == ("shell", "input", "keyevent", "KEYCODE_HOME"):
            self.layer = 0
            self.keyboard = False
            self.log.extend(["paper: drawn 320x640", "paper: apps 1, Paper listed true", "paper: layer 0"])
        elif args[:3] == ("shell", "input", "swipe"):
            self.drawers += 1
            self.layer = 1
            self.keyboard = not (self.missing_keyboard and self.drawers == 2)
            self.log.append("paper: layer 1")
        elif args[:3] == ("shell", "input", "tap"):
            self.keyboard = True
        elif args[:3] == ("shell", "input", "text"):
            self.log.append("paper: launch dev.fern.paper/dev.fern.paper.Home")
        elif args == ("shell", "input", "keyevent", "KEYCODE_BACK"):
            self.back_events.append((self.drawers, self.keyboard))
            if self.keyboard:
                self.keyboard = self.stuck_keyboard and self.drawers == 2
                if self.early_close and self.drawers == 2:
                    self.layer = 0
                    self.log.append("paper: layer 0")
            elif not self.ignored_back and self.layer != 0:
                self.layer = 0
                self.log.append("paper: layer 0")
        elif args[:3] == ("shell", "atrace", "--async_stop"):
            return "B|12|frame"
        elif args == ("shell", "dumpsys", "input_method") and self.keyboard:
            return ("  mStartInputHistory:\n"
                    "    inputType=0x0 imeOptions=0x0\n"
                    "  mCurrentEditorInfo:\n"
                    f"    {self.editor}\n"
                    "    packageName=dev.fern.paper\n"
                    "  mServedInputConnection=connection\n")
        return ""

    def wait_for(self, pattern, timeout=30):
        return re.search(pattern, self.logcat())

    def logcat(self):
        return "\n".join(self.log)

    def frame(self, out, name, top_half=False):
        part = 0.943 if self.layer else 0.106
        if name == "back" and self.wrong_home:
            part = 0.943
        return 320, 640, part


class BackNavigationTests(unittest.TestCase):
    def run_device(self, device):
        with tempfile.TemporaryDirectory() as out, patch.multiple(check, adb=device.adb, logcat=device.logcat,
                            keyboard_shown=lambda timeout=5.0: device.keyboard,
                            wait_for_keyboard=lambda shown, timeout=5.0: device.keyboard == shown,
                            wait_for=device.wait_for, paper_part=device.frame,
                            open_clock=lambda: None, finish=lambda: None,
                            fail=device.failures.append), patch.object(check.time, "sleep"):
            check.main("paper.apk", out)
        return device.failures

    def test_keyboard_hide_waits_for_the_state_change(self):
        with patch.object(check, "adb", side_effect=["mInputShown=true", "mInputShown=false"]) as calls, \
                patch.object(check.time, "sleep"):
            self.assertTrue(check.wait_for_keyboard(False, timeout=1.5))
        self.assertEqual(calls.call_count, 2)

    def test_keyboard_hide_timeout_is_a_failure(self):
        with patch.object(check, "adb", return_value="mInputShown=true"), \
                patch.object(check.time, "monotonic", side_effect=[0.0, 0.0, 0.5, 1.0]), \
                patch.object(check.time, "sleep"):
            self.assertFalse(check.wait_for_keyboard(False, timeout=1.0))

    def test_back_hides_keyboard_before_it_closes_the_drawer(self):
        device = Device()
        self.assertEqual(self.run_device(device), [])
        self.assertEqual(device.back_events, [(1, True), (2, True), (2, False)])
        self.assertEqual(device.layer, 0)

    def test_keyboard_failure_cannot_pass_as_app_navigation(self):
        failures = self.run_device(Device(stuck_keyboard=True))
        self.assertIn("the back key did not hide the keyboard", failures)

    def test_missing_drawer_keyboard_still_fails(self):
        failures = self.run_device(Device(missing_keyboard=True))
        self.assertIn("the app list opened without the keyboard", failures)

    def test_stale_home_log_cannot_hide_an_ignored_back(self):
        failures = self.run_device(Device(ignored_back=True))
        self.assertIn("the back key did not go back to the home screen", failures)

    def test_keyboard_back_must_leave_the_drawer_open(self):
        failures = self.run_device(Device(early_close=True))
        self.assertTrue(any("app list" in failure for failure in failures), failures)

    def test_keyboard_must_type_into_the_text_field(self):
        failures = self.run_device(Device(editor="inputType=0x0 imeOptions=0x0"))
        self.assertIn("the keyboard does not type into the text field: inputType=0x0 imeOptions=0x0", failures)

    def check_editor(self, dump):
        failures = []
        with patch.object(check, "adb", return_value=dump), patch.object(check, "fail", side_effect=failures.append):
            result = check.editor_is_field()
        return result, failures

    def test_current_editor_wins_over_the_decor_history(self):
        device = Device()
        device.keyboard = True
        dump = device.adb("shell", "dumpsys", "input_method")
        self.assertEqual(self.check_editor(dump), (True, []))

    def test_old_text_editor_cannot_hide_a_current_decor_editor(self):
        device = Device(editor="inputType=0x0 imeOptions=0x0")
        device.keyboard = True
        dump = device.adb("shell", "dumpsys", "input_method").replace(
            "  mStartInputHistory:\n    inputType=0x0 imeOptions=0x0",
            "  mStartInputHistory:\n    inputType=0x1 imeOptions=0x10000002")
        self.assertFalse(self.check_editor(dump)[0])

    def test_history_cannot_replace_a_missing_current_editor(self):
        for suffix in ("", "  mCurrentEditorInfo: null\n"):
            with self.subTest(suffix=suffix):
                dump = "  mStartInputHistory:\n    inputType=0x1 imeOptions=0x10000002\n" + suffix
                self.assertFalse(self.check_editor(dump)[0])

    def test_current_editor_must_belong_to_paper(self):
        device = Device()
        device.keyboard = True
        for package in ("dev.fern.paper.other", "another.app"):
            with self.subTest(package=package):
                dump = device.adb("shell", "dumpsys", "input_method").replace("packageName=dev.fern.paper", "packageName=" + package)
                self.assertFalse(self.check_editor(dump)[0])

    def test_current_editor_still_requires_the_go_action(self):
        device = Device(editor="inputType=0x1 imeOptions=0x10000006")
        device.keyboard = True
        self.assertFalse(self.check_editor(device.adb("shell", "dumpsys", "input_method"))[0])

    def test_focused_client_cannot_replace_a_null_current_editor(self):
        device = Device()
        device.keyboard = True
        dump = "  mCurrentEditorInfo: null\n" + device.adb("shell", "dumpsys", "input_method")
        self.assertFalse(self.check_editor(dump)[0])

    def test_home_transition_also_requires_the_home_frame(self):
        failures = self.run_device(Device(wrong_home=True))
        self.assertTrue(any("paper colour" in failure for failure in failures), failures)


if __name__ == "__main__":
    unittest.main()
