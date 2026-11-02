import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location(
    "android_app_syscalls", Path(__file__).with_name("android-app-syscalls.py")
)
android = importlib.util.module_from_spec(spec)
spec.loader.exec_module(android)


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
