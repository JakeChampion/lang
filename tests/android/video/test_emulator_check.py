"""Check media fixture permissions before video test startup."""

import importlib.util
import hashlib
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


class NativeAvDiagnosticsTests(unittest.TestCase):
    def log(self, first="unavailable", second="frames=0 monotonic_ns=0"):
        return ("W/fern: videotest: native AAC frames=19200 rate=48000 channels=2\n"
                "W/fern: videotest: native AV submitted_frames=19200 origin_us=0\n"
                f"W/fern: videotest: native AV timestamp {first}\n"
                "W/fern: videotest: native AV submitted_frames=14400 origin_us=100000\n"
                f"W/fern: videotest: native AV timestamp {second}\n")

    def test_available_and_unavailable_anchors_remain_distinct(self):
        for first, second in [('unavailable', 'frames=0 monotonic_ns=0'),
                              ('frames=19200 monotonic_ns=9223372036854775807', 'unavailable'),
                              ('unavailable', 'unavailable')]:
            with self.subTest(first=first, second=second):
                result = check.native_av_metrics(self.log(first, second))
                self.assertEqual(result['frames'], 19200)
                self.assertEqual(result['timestamps'][0] is None, first == 'unavailable')
                self.assertEqual(result['timestamps'][1] is None, second == 'unavailable')

    def test_invalid_timestamp_diagnostics_fail(self):
        for value in ['frames=-1 monotonic_ns=0', 'frames=0 monotonic_ns=-1',
                      'frames=0 monotonic_ns=9223372036854775808', 'frames=no monotonic_ns=1', '']:
            with self.subTest(value=value), self.assertRaises(AssertionError):
                check.native_av_metrics(self.log(second=value))

    def test_audio_counts_and_seek_origins_must_match(self):
        for before, after in [('channels=2', 'channels=1'), ('rate=48000', 'rate=44100'),
                              ('frames=19200 rate', 'frames=19456 rate'),
                              ('submitted_frames=14400', 'submitted_frames=14399'),
                              ('origin_us=100000', 'origin_us=0')]:
            with self.subTest(before=before), self.assertRaises(AssertionError):
                check.native_av_metrics(self.log().replace(before, after))

    def test_private_av_launch_needs_no_storage_grant(self):
        def device(*args, **kwargs):
            if args == ("shell", "getprop", "ro.build.version.sdk"):
                return "34\n"
            if args[:3] == ("shell", "am", "start"):
                raise RuntimeError("launch sentinel")
            return ""

        with tempfile.TemporaryDirectory() as directory, patch.object(check, "adb", side_effect=device) as calls:
            with self.assertRaisesRegex(RuntimeError, "launch sentinel"):
                check.main("video.apk", directory, av_path="/data/user/0/dev.fern.videotest/files/native-av.mp4")
            self.assertFalse(any(call.args[:3] == ("shell", "pm", "grant") for call in calls.call_args_list))
            start = next(call.args for call in calls.call_args_list if call.args[:3] == ("shell", "am", "start"))
            self.assertEqual(start[-3:], ('--es', 'av_path', '/data/user/0/dev.fern.videotest/files/native-av.mp4'))


class NativeContentDiagnosticsTests(unittest.TestCase):
    data = b'native content bytes\x00\xff'

    def line(self, count=None, digest=None):
        if count is None:
            count = len(self.data)
        if digest is None:
            digest = hashlib.sha256(self.data).hexdigest()
        return f'W/fern: videotest: native content bytes={count} sha256={digest}\n'

    def test_native_bytes_match_the_independent_content_read(self):
        value = check.native_content_metrics(self.line(), self.data)
        self.assertEqual(value, {'bytes': len(self.data), 'sha256': hashlib.sha256(self.data).hexdigest()})

    def test_missing_duplicate_or_malformed_diagnostics_fail(self):
        for log in ['', self.line() * 2, self.line(count=-1), self.line(digest='0' * 63),
                    self.line(digest='Z' * 64), self.line().replace('sha256=', 'sha256=0')]:
            with self.subTest(log=log), self.assertRaises(AssertionError):
                check.native_content_metrics(log, self.data)

    def test_wrong_byte_count_or_digest_fails(self):
        for log in [self.line(count=len(self.data) + 1), self.line(digest='0' * 64)]:
            with self.subTest(log=log), self.assertRaises(AssertionError):
                check.native_content_metrics(log, self.data)

    def test_independent_reference_must_fit_the_fixture_limit(self):
        for expected in [b'', b'x' * 2097153]:
            with self.subTest(length=len(expected)), self.assertRaises(AssertionError):
                check.native_content_metrics(self.line(), expected)


if __name__ == "__main__":
    unittest.main()
