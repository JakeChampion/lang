"""Check media fixture permissions before video test startup."""

import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("video_check", Path(__file__).with_name("emulator_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class ContentPermissionTests(unittest.TestCase):
    def test_content_fixture_uses_the_platform_media_permission(self):
        for sdk in (29, 32, 33, 35):
            def device(*args, **kwargs):
                if args == ("shell", "getprop", "ro.build.version.sdk"):
                    return str(sdk)
                if args[:3] == ("shell", "am", "start"):
                    raise RuntimeError("launch sentinel")
                return ""

            with self.subTest(sdk=sdk), tempfile.TemporaryDirectory() as directory, \
                    patch.object(check, "adb", side_effect=device) as calls:
                with self.assertRaisesRegex(RuntimeError, "launch sentinel"):
                    check.main("video.apk", directory, "content://media/external/video/media/42")
                permission = "READ_MEDIA_VIDEO" if sdk >= 33 else "READ_EXTERNAL_STORAGE"
                grant = ("shell", "pm", "grant", check.PACKAGE, "android.permission." + permission)
                calls.assert_any_call(*grant)
                commands = [call.args for call in calls.call_args_list]
                start = next(command for command in commands if command[:3] == ("shell", "am", "start"))
                self.assertLess(commands.index(grant), commands.index(start))
                self.assertNotIn("--grant-read-uri-permission", start)

    def test_permission_failure_prevents_content_launch(self):
        def device(*args, **kwargs):
            if args == ("shell", "getprop", "ro.build.version.sdk"):
                return "34\n"
            if args[:3] == ("shell", "pm", "grant"):
                raise subprocess.CalledProcessError(1, "permission grant")
            return ""

        with tempfile.TemporaryDirectory() as directory, patch.object(check, "adb", side_effect=device) as calls:
            with self.assertRaises(subprocess.CalledProcessError):
                check.main("video.apk", directory, "content://media/external/video/media/42")
            self.assertFalse(any(call.args[:3] == ("shell", "am", "start") for call in calls.call_args_list))

    def test_private_video_round_needs_no_media_permission(self):
        def device(*args, **kwargs):
            if args == ("shell", "getprop", "ro.build.version.sdk"):
                return "34\n"
            if args[:3] == ("shell", "am", "start"):
                raise RuntimeError("launch sentinel")
            return ""

        with tempfile.TemporaryDirectory() as directory, patch.object(check, "adb", side_effect=device) as calls:
            with self.assertRaisesRegex(RuntimeError, "launch sentinel"):
                check.main("video.apk", directory)
            self.assertFalse(any(call.args[:3] == ("shell", "pm", "grant") for call in calls.call_args_list))


if __name__ == "__main__":
    unittest.main()
