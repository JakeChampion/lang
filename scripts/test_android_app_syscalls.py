import base64
import importlib.util
import io
from pathlib import Path
import unittest
import urllib.error


spec = importlib.util.spec_from_file_location(
    "android_app_syscalls", Path(__file__).with_name("android-app-syscalls.py")
)
android = importlib.util.module_from_spec(spec)
spec.loader.exec_module(android)


class FetchTest(unittest.TestCase):
    def opener(self, codes):
        def urlopen(url, timeout):
            code = codes.pop(0)
            if code != 200:
                raise urllib.error.HTTPError(url, code, "busy", {}, None)
            return io.BytesIO(base64.b64encode(b"text"))
        return urlopen

    def test_retries_a_busy_server(self):
        waits = []
        got = android.fetch("SYSCALLS.TXT", sleep=waits.append, urlopen=self.opener([503, 429, 200]))
        self.assertEqual(got, "text")
        self.assertEqual(waits, [5, 15])

    def test_gives_up_after_the_last_wait(self):
        waits = []
        with self.assertRaises(urllib.error.HTTPError):
            android.fetch("SYSCALLS.TXT", sleep=waits.append, urlopen=self.opener([503] * 5))
        self.assertEqual(waits, [5, 15, 45, 90])

    def test_does_not_retry_a_missing_page(self):
        with self.assertRaises(urllib.error.HTTPError):
            android.fetch("SYSCALLS.TXT", sleep=lambda s: None, urlopen=self.opener([404, 200]))


class SyscallNamesTest(unittest.TestCase):
    def test_nested_function_pointer_parameter(self):
        row = "pid_t\tclone(int (*)(void*), void*, int, void*, ...) all"
        for arch in ("arm64", "x86_64"):
            with self.subTest(arch=arch):
                self.assertEqual(android.names(row, arch), {"clone"})

    def test_syscall_names_aliases_and_architectures(self):
        rows = """
        int __openat:openat(int, const char*, int, mode_t) all
        int read|__read_chk(int, void*, size_t) lp64
        int legacy(int) arm,x86
        """
        self.assertEqual(android.names(rows, "arm64"), {"openat", "read"})
        self.assertEqual(android.names(rows, "x86"), {"openat", "legacy"})

    def test_invalid_row_is_an_error(self):
        with self.assertRaises(ValueError):
            android.names("this is not a syscall row", "arm64")


if __name__ == "__main__":
    unittest.main()
