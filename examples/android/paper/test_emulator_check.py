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
                 editor="inputType=0x1 imeOptions=0x10000002",
                 dump='<node index="2" text="9:06" resource-id="" class="android.widget.Button" package="dev.fern.paper" content-desc="" clickable="true" />'):
        self.dump = dump
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
        elif args == ("shell", "cat", "/sdcard/paper-ui.xml"):
            return self.dump
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
                            check_filter=lambda out, width, height: None,
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

    def test_clock_must_be_a_button_for_accessibility(self):
        failures = self.run_device(Device(dump='<node text="9:06" class="android.view.View" package="dev.fern.paper" clickable="false" />'))
        self.assertIn("the accessibility layer has no clock button of Paper", failures)

    def test_home_transition_also_requires_the_home_frame(self):
        failures = self.run_device(Device(wrong_home=True))
        self.assertTrue(any("paper colour" in failure for failure in failures), failures)


def node(text, box, clickable="true"):
    return f'<node text="{text}" class="android.widget.Button" clickable="{clickable}" bounds="[{box[0]},{box[1]}][{box[2]},{box[3]}]" />'


class Phone:
    """A device for check_filter. `holds` is whether the filter takes a
    notification out of the shade, and `shows` is whether Paper shows it."""

    def __init__(self, *, holds=True, shows=True, opens=True, sends=True, takes_old=False, filter_row="off", probe=True):
        self.holds = holds
        self.shows = shows
        self.opens = opens
        self.sends = sends
        self.front = "dev.fern.paper"
        self.takes_old = takes_old
        self.filter_row = filter_row
        self.probe = probe
        self.allowed = False
        # adb runs as root, and the device drops a notification that root
        # posts.
        self.dropped = []
        self.on = filter_row == "on"
        self.screen = "home"
        self.shade = []
        self.held = []
        self.log = []
        self.failures = []

    def post(self, tag):
        if self.allowed and self.on and self.shows:
            self.held.append(tag)
        if not (self.allowed and self.on and self.holds):
            self.shade.append(tag)

    def tap(self, x, y):
        if self.screen == "settings" and y == 150:
            self.on = True
            self.screen = "home"
            if self.takes_old:
                self.shade.clear()
        elif self.screen == "home" and y == 450 and self.held:
            self.screen = "held"
        elif self.screen == "held" and y == 150:
            self.screen = "home"
            if self.opens:
                self.held.clear()
                self.log.append("paper: open held com.android.shell")
                if self.sends:
                    self.front = "com.android.settings"

    def dump(self):
        if self.screen == "settings":
            return node("Notification filter: " + self.filter_row, (0, 100, 320, 200))
        if self.screen == "held":
            return node("Shell · 9:06&#10;Fern: held_text", (0, 100, 320, 200))
        nodes = node("9:06", (0, 0, 320, 300))
        if self.held:
            nodes += node(f"{len(self.held)} held notification", (0, 400, 320, 500))
        return nodes

    def adb(self, *args, **kwargs):
        if args == ("shell", "id", "-u"):
            return "0\n"
        if args[:4] == ("shell", "cmd", "notification", "post"):
            self.dropped.append(args[-2])
        elif args[:6] == ("shell", "su", "shell", "cmd", "notification", "post"):
            self.post(args[-2])
        elif args[:4] == ("shell", "cmd", "notification", "allow_listener"):
            self.allowed = True
        elif args[:3] == ("shell", "input", "swipe") and args[3:5] == args[5:7]:
            self.screen = "settings"
        elif args[:3] == ("shell", "input", "tap"):
            self.tap(int(args[3]), int(args[4]))
        elif args == ("shell", "input", "keyevent", "KEYCODE_BACK"):
            self.screen = "home"
        elif args == ("shell", "input", "keyevent", "KEYCODE_HOME"):
            self.front = "dev.fern.paper"
        elif args[:4] == ("shell", "dumpsys", "activity", "activities"):
            return f"  topResumedActivity=ActivityRecord{{1 u0 {self.front}/.Main t1}}\n"
        elif args[:4] == ("shell", "cmd", "notification", "list") and self.probe:
            return "".join(f"0|com.android.shell|2020|{tag}|2000\n" for tag in self.shade)
        elif args == ("shell", "cat", "/sdcard/paper-ui.xml"):
            return self.dump()
        return ""

    def wait_for(self, pattern, timeout=30):
        return re.search(pattern, "\n".join(self.log))


class FilterTests(unittest.TestCase):
    def run_phone(self, phone):
        clock = iter(range(0, 100000, 5))
        with tempfile.TemporaryDirectory() as out, patch.multiple(check, adb=phone.adb, wait_for=phone.wait_for,
                            shell=lambda *args: phone.adb("shell", *args),
                            logcat=lambda: "\n".join(phone.log),
                            fail=phone.failures.append), patch.object(check.time, "sleep"), \
                patch.object(check.time, "monotonic", side_effect=lambda: next(clock)):
            check.check_filter(out, 320, 640)
        return phone.failures

    def test_a_held_notification_leaves_the_shade_and_opens(self):
        phone = Phone()
        self.assertEqual(self.run_phone(phone), [])
        self.assertEqual(phone.shade, ["fern_shown"])
        self.assertFalse(phone.allowed and phone.held)

    def test_each_post_is_a_post_of_the_shell_user_when_adb_is_root(self):
        phone = Phone()
        self.assertEqual(self.run_phone(phone), [])
        self.assertEqual(phone.dropped, [])

    def test_adb_that_is_not_root_posts_with_no_change_of_user(self):
        with patch.object(check, "shell", side_effect=["2000\n"]):
            self.assertEqual(check.poster(), [])

    def test_a_filter_that_is_on_already_stays_on(self):
        self.assertEqual(self.run_phone(Phone(filter_row="on")), [])

    def test_a_notification_that_stays_in_the_shade_fails(self):
        failures = self.run_phone(Phone(holds=False))
        self.assertIn("the filter did not take the notification out of the shade", failures)

    def test_a_notification_that_paper_does_not_show_fails(self):
        failures = self.run_phone(Phone(shows=False))
        self.assertEqual(failures, ["the home screen does not show a held notification"])

    def test_a_notification_that_paper_does_not_show_keeps_the_answers_of_the_device(self):
        phone = Phone(shows=False)
        clock = iter(range(0, 100000, 5))
        with tempfile.TemporaryDirectory() as out, patch.multiple(check, adb=phone.adb, wait_for=phone.wait_for,
                            shell=lambda *args: phone.adb("shell", *args) or "answer to " + " ".join(args[:2]),
                            logcat=lambda: "paper: drawn 320x640", fail=phone.failures.append), patch.object(check.time, "sleep"), \
                patch.object(check.time, "monotonic", side_effect=lambda: next(clock)):
            check.check_filter(out, 320, 640)
            kept = {p.name: p.read_text() for p in Path(out).iterdir()}
        self.assertEqual(kept["held-listeners.txt"], "answer to settings get")
        self.assertEqual(kept["held-held-file.txt"], "answer to cat /data/data/dev.fern.paper/files/held")
        self.assertEqual(kept["held-log.txt"], "paper: drawn 320x640")
        self.assertIn("9:06", kept["held-texts.txt"])

    def test_a_held_notification_that_does_not_open_fails(self):
        failures = self.run_phone(Phone(opens=False))
        self.assertIn("a tap on the held notification did not open it", failures)
        self.assertIn("the held notification stayed on the home screen after it opened", failures)

    def test_a_held_notification_whose_intent_opens_nothing_fails(self):
        failures = self.run_phone(Phone(sends=False))
        self.assertEqual(failures, ["the intent of the held notification did not open the settings of Android"])

    def test_the_tag_of_another_notification_is_not_in_the_shade(self):
        with patch.object(check, "adb", return_value="0|com.android.shell|2020|fern_held_more|2000\n"):
            self.assertFalse(check.in_shade("fern_held"))
            self.assertTrue(check.in_shade("fern_held_more"))

    def test_a_notification_from_before_the_filter_stays(self):
        failures = self.run_phone(Phone(takes_old=True))
        self.assertIn("the filter took a notification from before it was on", failures)

    def test_a_shade_that_the_check_cannot_read_fails(self):
        failures = self.run_phone(Phone(probe=False))
        self.assertEqual(failures, ["the shade does not show a notification that the shell posted"])

    def test_a_shade_that_the_check_cannot_read_keeps_the_answers_of_the_device(self):
        phone = Phone(probe=False)
        with tempfile.TemporaryDirectory() as out, patch.multiple(check, adb=phone.adb, fail=phone.failures.append,
                            shell=lambda *args: "answer to " + " ".join(args[:3])), patch.object(check.time, "sleep"), \
                patch.object(check.time, "monotonic", side_effect=iter(range(0, 1000, 5)).__next__):
            check.check_filter(out, 320, 640)
            kept = {p.name: p.read_text() for p in Path(out).iterdir()}
        self.assertEqual(kept["shade-list.txt"], "answer to cmd notification list")
        self.assertEqual(kept["shade-post.txt"], "answer to cmd notification post")
        self.assertIn("shade-dumpsys.txt", kept)

    def test_find_node_gives_the_middle_of_the_node(self):
        dump = node("Other", (0, 0, 10, 10)) + node("Notification filter: off", (0, 100, 320, 200))
        self.assertEqual(check.find_node(dump, "Notification filter: (?:on|off)"), (160, 150, "Notification filter: off"))
        self.assertIsNone(check.find_node(dump, "Absent"))

    def test_free_point_is_outside_each_node_with_an_action(self):
        dump = node("9:06", (0, 0, 320, 300)) + node("wall", (0, 0, 320, 640), clickable="false")
        x, y = check.free_point(dump, 320, 640)
        self.assertGreater(y, 300)
        self.assertIsNone(check.free_point(node("all", (0, 0, 320, 640)), 320, 640))


if __name__ == "__main__":
    unittest.main()
