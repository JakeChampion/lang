import importlib.util
from pathlib import Path
import unittest
import zlib


spec = importlib.util.spec_from_file_location("files_emulator_check", Path(__file__).with_name("emulator_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


def tree(*rows):
    body = "".join(f'<node package="{package}" text="{text}" content-desc="{desc}" bounds="{bounds}" />'
                   for package, text, desc, bounds in rows)
    return f"<hierarchy><node package='android' bounds='[0,0][1080,2400]'>{body}</node></hierarchy>"


class NodeTests(unittest.TestCase):
    def test_text_and_description(self):
        xml = tree(("dev.fern.files", "Open", "", "[100,200][300,400]"),
                   ("dev.fern.files", "", "Phone storage", "[0,500][1080,700]"))
        self.assertEqual(check.point_of(xml, "Open"), (200, 300))
        self.assertEqual(check.point_of(xml, "Phone storage"), (540, 600))

    def test_other_apps_and_empty_bounds_do_not_match(self):
        xml = tree(("com.android.launcher3", "Open", "", "[100,200][300,400]"),
                   ("dev.fern.files", "Delete", "", "[0,0][0,0]"),
                   ("dev.fern.files", "Copy", "", ""))
        for label in ("Open", "Delete", "Copy", "Paste here"):
            with self.subTest(label=label):
                self.assertIsNone(check.point_of(xml, label))

    def test_only_the_whole_name_matches(self):
        xml = tree(("dev.fern.files", "Confirm delete", "", "[0,0][10,10]"))
        self.assertIsNone(check.point_of(xml, "Delete"))
        self.assertIsNone(check.point_of(xml, "delete"))

    def test_text_that_is_not_xml(self):
        self.assertEqual(check.nodes("ERROR: could not get idle state."), [])
        self.assertEqual(check.nodes(""), [])


class ScreenTests(unittest.TestCase):
    def test_physical_size(self):
        self.assertEqual(check.screen_size("Physical size: 1080x2400\n"), (1080, 2400))

    def test_override_wins(self):
        self.assertEqual(check.screen_size("Physical size: 1080x2400\nOverride size: 720x1600\n"), (720, 1600))

    def test_no_size(self):
        self.assertIsNone(check.screen_size("error: device offline"))


class TrashTests(unittest.TestCase):
    def test_entry_of_the_file(self):
        listing = ".\n..\n1795000000~L3N0b3JhZ2U~other.txt\n1795000001~L3N0b3JhZ2U~notes.txt\n"
        self.assertEqual(check.trash_entry(listing, "notes.txt"), "1795000001~L3N0b3JhZ2U~notes.txt")

    def test_a_name_with_the_separator(self):
        self.assertEqual(check.trash_entry("17~QQ~a~b.txt\n", "a~b.txt"), "17~QQ~a~b.txt")

    def test_no_entry(self):
        for listing in ["", "notes.txt\n", "x~QQ~notes.txt\n", "17~QQ~notes.txt.bak\n", "ls: /sdcard/.FernTrash: No such file or directory\n"]:
            with self.subTest(listing=listing):
                self.assertIsNone(check.trash_entry(listing, "notes.txt"))


class FileTests(unittest.TestCase):
    def test_digest(self):
        self.assertEqual(check.digest("abc123  /storage/emulated/0/Pictures/dot.png\n"), "abc123")
        self.assertEqual(check.digest(""), "")

    def test_png(self):
        data = check.png(3, 2, (215, 25, 33))
        self.assertEqual(data[:8], b"\x89PNG\r\n\x1a\n")
        self.assertEqual(data[16:24], b"\x00\x00\x00\x03\x00\x00\x00\x02")
        at = data.index(b"IDAT")
        size = int.from_bytes(data[at - 4:at], "big")
        self.assertEqual(zlib.decompress(data[at + 4:at + 4 + size]), (b"\x00" + bytes((215, 25, 33)) * 3) * 2)


if __name__ == "__main__":
    unittest.main()
