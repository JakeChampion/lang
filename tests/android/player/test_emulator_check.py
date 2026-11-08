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
