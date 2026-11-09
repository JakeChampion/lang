"""Check MediaStore item validation and binary fixture transport."""

import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location("player_check", Path(__file__).with_name("emulator_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class ContentSourceTests(unittest.TestCase):
    def test_content_fixture_uses_the_platform_media_permission(self):
        for sdk in (26, 32, 33, 35):
            def device(*args, **kwargs):
                if args == ("shell", "getprop", "ro.build.version.sdk"):
                    return str(sdk)
                if args[:3] == ("logcat", "-d", "-v") and len(args) > 4:
                    return "playertest: FAIL fixture sentinel\n"
                return ""

            with self.subTest(sdk=sdk), tempfile.TemporaryDirectory() as directory, \
                    patch.object(check, "adb", side_effect=device) as calls:
                with self.assertRaisesRegex(AssertionError, "fixture sentinel"):
                    check.run_playback("content://media/external/video/media/42", "http://missing", Path(directory))
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
                check.run_playback("content://media/external/video/media/42", "http://missing", Path(directory))
            self.assertFalse(any(call.args[:3] == ("shell", "am", "start") for call in calls.call_args_list))

    def test_diagnostic_and_cleanup_failures_preserve_the_playback_failure(self):
        stops = 0

        def device(*args, **kwargs):
            nonlocal stops
            if args == ("shell", "getprop", "ro.build.version.sdk"):
                return "34\n"
            if args[:3] == ("shell", "am", "force-stop"):
                stops += 1
                if stops == 2:
                    raise subprocess.CalledProcessError(1, "force-stop")
            if args == ("shell", "dumpsys", "activity", "permissions"):
                raise OSError("permission diagnostic unavailable")
            if args == ("logcat", "-d", "-v", "time"):
                return "platform source details\n"
            if args[:3] == ("logcat", "-d", "-v"):
                return "playertest: FAIL source\n"
            return ""

        with tempfile.TemporaryDirectory() as directory, patch.object(check, "adb", side_effect=device):
            out = Path(directory)
            with self.assertRaisesRegex(AssertionError, "playertest: FAIL source"):
                check.run_playback("content://media/external/video/media/42", "http://missing", out)
            self.assertEqual(stops, 2)
            self.assertEqual((out / "logcat.txt").read_text(), "playertest: FAIL source\n")
            self.assertEqual((out / "logcat-all.txt").read_text(), "platform source details\n")
            self.assertTrue((out / "media-player-final.txt").exists())

    def test_cleanup_failure_after_success_remains_a_failure(self):
        stops = 0
        logs = iter(("playertest: paused position_ms=1\nplayertest: seeked\n", "\n".join(
            "playertest: " + marker for marker in (
                "opened", "prepared round=0", "paused position_ms=1", "pause stable",
                "seeked position_ms=1", "timestamp media_us=1", "completed round=0",
                "prepared round=1", "completed round=1", "expected error", "passed"))))

        def device(*args, **kwargs):
            nonlocal stops
            if args[:3] == ("shell", "am", "force-stop"):
                stops += 1
                if stops == 2:
                    raise subprocess.CalledProcessError(1, "force-stop")
            if args == ("exec-out", "screencap", "-p"):
                return b"screen"
            if args[:3] == ("logcat", "-d", "-v") and len(args) > 4:
                return next(logs)
            return ""

        with tempfile.TemporaryDirectory() as directory, \
                patch.object(check, "adb", side_effect=device), \
                patch.object(check, "verify_screen") as screen, \
                patch.object(check.time, "sleep"):
            with self.assertRaises(subprocess.CalledProcessError):
                check.run_playback("http://source", "http://missing", Path(directory))
            screen.assert_called_once_with(b"screen")
            self.assertEqual(stops, 2)

    def test_insert_accepts_only_a_created_video_item(self):
        with patch.object(check, "adb", side_effect=["", "Row: 0 _id=42\n"]):
            self.assertEqual(check.insert_media(), "content://media/external/video/media/42")

    def test_insert_rejects_collections_and_diagnostic_uris(self):
        # A collection URI must never reach the cleanup delete command.
        for output in (
            "No result found.",
            "Error accessing content://media/external/video/media/42",
            "content://media/external/video/media/42",
            "Row: 0 _id=42\nRow: 1 _id=43",
            "Row: 0 _id=null",
            "Row: 0 _id=-1",
            "Row: 0 _id=42 unexpected",
        ):
            with self.subTest(output=output), patch.object(check, "adb", side_effect=["", output]):
                with self.assertRaises(AssertionError):
                    check.insert_media()

    def test_video_failure_removes_only_the_two_created_items(self):
        first = "content://media/external/video/media/42"
        second = "content://media/external/video/media/43"
        server = Mock(server_address=("127.0.0.1", 12345))
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(check, "write_fixture"), \
                patch.object(check, "handler_for"), \
                patch.object(check, "ThreadingHTTPServer", return_value=server), \
                patch.object(check.threading, "Thread"), \
                patch.object(check, "run_playback"), \
                patch.object(check, "insert_media", side_effect=[first, second]), \
                patch.object(check, "write_media"), \
                patch.object(check, "adb") as device, \
                patch.object(check.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "video")):
            with self.assertRaises(subprocess.CalledProcessError):
                check.main("player.apk", directory, "video.apk", "byte.mp4")
            deletions = [call.args[-1] for call in device.call_args_list
                         if call.args[:3] == ("shell", "content", "delete")]
            self.assertEqual(deletions, [second, first])
            device.assert_any_call("reverse", "--remove", "tcp:12345")
            server.shutdown.assert_called_once()
            server.server_close.assert_called_once()

    def test_binary_upload_requires_exact_provider_readback(self):
        data = bytes(range(256)) + b"\x00\r\n\xff"
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "fixture.mp4"
            path.write_bytes(data)
            with patch.object(check.subprocess, "run") as send, patch.object(check, "adb", return_value=data):
                check.write_media("content://media/external/video/media/42", path)
                self.assertEqual(send.call_args.kwargs["input"], data)
            with patch.object(check.subprocess, "run"), patch.object(check, "adb", return_value=data[:-1]):
                with self.assertRaises(AssertionError):
                    check.write_media("content://media/external/video/media/42", path)


if __name__ == "__main__":
    unittest.main()
