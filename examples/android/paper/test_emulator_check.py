"""Check Back routing without weakening keyboard or home-screen assertions."""
import importlib.util
from pathlib import Path
import re
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("paper_emulator_check", Path(__file__).with_name("emulator_check.py"))
check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check)


class BackNavigationTest(unittest.TestCase):
    def drive(self, *, close_on_first=False, closes=True, hides=True):
        state = {"log": "paper: layer 0", "backs": 0, "keyboard": True}

        def device(*args, **kwargs):
            if args == ("logcat", "-c"):
                state["log"] = ""
            elif args == ("shell", "input", "keyevent", "KEYCODE_BACK"):
                state["backs"] += 1
                if hides:
                    state["keyboard"] = False
                if closes and (close_on_first or state["backs"] == 2):
                    state["log"] = "paper: layer 0"
            return ""

        with patch.object(check, "adb", side_effect=device), \
                patch.object(check, "keyboard_state", side_effect=lambda shown: state["keyboard"] == shown), \
                patch.object(check, "logcat", side_effect=lambda: state["log"]), \
                patch.object(check, "wait_for", side_effect=lambda pattern, **kwargs: re.search(pattern, state["log"])), \
                patch.object(check, "fail") as failed:
            passed = check.close_drawer()
        return passed, state, failed

    def test_keyboard_consumes_first_back_then_the_drawer_closes(self):
        passed, state, failed = self.drive()
        self.assertTrue(passed)
        self.assertEqual(state["backs"], 2)
        failed.assert_not_called()

    def test_first_back_can_hide_the_keyboard_and_close_the_drawer(self):
        passed, state, failed = self.drive(close_on_first=True)
        self.assertTrue(passed)
        self.assertEqual(state["backs"], 1)
        failed.assert_not_called()

    def test_a_stale_home_log_cannot_hide_failed_navigation(self):
        passed, state, failed = self.drive(closes=False)
        self.assertFalse(passed)
        self.assertEqual(state["backs"], 2)
        failed.assert_called_once_with("the back key did not go back to the home screen")

    def test_keyboard_failure_prevents_another_back(self):
        passed, state, failed = self.drive(hides=False)
        self.assertFalse(passed)
        self.assertEqual(state["backs"], 1)
        failed.assert_called_once_with("the back key did not hide the keyboard before navigation")

    def test_keyboard_transition_waits_for_the_requested_state(self):
        with patch.object(check, "adb", side_effect=["mInputShown=true", "mInputShown=true", "mInputShown=false"]), \
                patch.object(check.time, "monotonic", side_effect=[0, 0, 0.5, 1.0]), \
                patch.object(check.time, "sleep"):
            self.assertTrue(check.keyboard_state(False, 1.5))

    def test_keyboard_timeout_still_fails(self):
        with patch.object(check, "adb", return_value="mInputShown=true"), \
                patch.object(check.time, "monotonic", side_effect=[0, 0, 0.5, 1.5]), \
                patch.object(check.time, "sleep"):
            self.assertFalse(check.keyboard_state(False, 1.5))


if __name__ == "__main__":
    unittest.main()
