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


def fields(*rows):
    body = "".join(f'<node package="dev.fern.files" text="{text}" content-desc="{desc}" clickable="{click}" bounds="{bounds}" />'
                   for text, desc, click, bounds in rows)
    return f"<hierarchy><node package='android' bounds='[0,0][1080,2400]'>{body}</node></hierarchy>"


class FieldTests(unittest.TestCase):
    def test_empty_field_has_its_hint(self):
        xml = fields(("", "Leave empty for a guest", "true", "[0,400][1080,560]"))
        self.assertEqual(check.field_point(xml, "Leave empty for a guest"), (540, 480))

    def test_field_with_text(self):
        xml = fields(("", "Leave empty for a guest, jo", "true", "[0,400][1080,560]"))
        self.assertEqual(check.field_point(xml, "Leave empty for a guest"), (540, 480))

    def test_a_label_with_the_same_words_is_not_the_field(self):
        for rows in [(("Password", "", "false", "[0,300][1080,360]"), ("", "Password", "true", "[0,400][1080,560]")),
                     (("", "Password", "true", "[0,400][1080,560]"), ("Password", "", "false", "[0,300][1080,360]"))]:
            with self.subTest(rows=rows):
                self.assertEqual(check.field_point(fields(*rows), "Password"), (540, 480))

    def test_no_field(self):
        xml = fields(("Passwords", "", "true", "[0,300][1080,360]"), ("Password", "", "false", "[0,300][1080,360]"))
        self.assertIsNone(check.field_point(xml, "Password"))

    def test_keyboard(self):
        self.assertTrue(check.keyboard_shown("  mShowRequested=true mInputShown=true mInFullscreenMode=false\n"))
        self.assertFalse(check.keyboard_shown("  mShowRequested=false mInputShown=false\n"))
        self.assertFalse(check.keyboard_shown(""))


class CrashTests(unittest.TestCase):
    def test_block_of_one_process(self):
        log = ("I/fern    ( 812): files: started\n"
               "E/AndroidRuntime( 8659): FATAL EXCEPTION: main\n"
               "E/AndroidRuntime( 8659): Process: dev.fern.files, PID: 8659\n"
               "E/AndroidRuntime( 8659): java.lang.NullPointerException\n"
               "E/AndroidRuntime(  901): other\n"
               "E/AndroidRuntime( 8659): at x\n")
        self.assertEqual(check.crash_blocks(log), ["E/AndroidRuntime( 8659): FATAL EXCEPTION: main\n"
                                                   "E/AndroidRuntime( 8659): Process: dev.fern.files, PID: 8659\n"
                                                   "E/AndroidRuntime( 8659): java.lang.NullPointerException"])

    def test_no_crash(self):
        self.assertEqual(check.crash_blocks("E/AndroidRuntime( 1): something\n"), [])


class WindowTests(unittest.TestCase):
    def test_focused_window(self):
        dump = ("  mCurrentFocus=Window{9f1c2d u0 com.android.permissioncontroller/com.android.permissioncontroller.permission.ui.GrantPermissionsActivity}\n"
                "  mFocusedApp=ActivityRecord{1 u0 dev.fern.files/android.app.NativeActivity t9}\n")
        self.assertEqual(check.focused_window(dump),
                         "com.android.permissioncontroller/com.android.permissioncontroller.permission.ui.GrantPermissionsActivity")

    def test_no_focus(self):
        self.assertEqual(check.focused_window("  mCurrentFocus=null\n"), "")


class DocumentTests(unittest.TestCase):
    def test_root_of_a_server(self):
        self.assertEqual(check.document_uri("0:", children=True),
                         "content://dev.fern.files.Documents/document/0%3A/children")

    def test_file_in_a_folder(self):
        self.assertEqual(check.document_uri("2:Films/a b.mkv"),
                         "content://dev.fern.files.Documents/document/2%3AFilms%2Fa%20b.mkv")


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


class SwipeTests(unittest.TestCase):
    def test_half_the_screen(self):
        self.assertEqual(check.swipe_up(tree()), ((540, 1800), (540, 600)))

    def test_no_screen(self):
        for xml in ("", "ERROR: could not get idle state.", "<hierarchy />",
                    "<hierarchy><node bounds='[0,0][0,0]' /></hierarchy>"):
            with self.subTest(xml=xml):
                self.assertIsNone(check.swipe_up(xml))


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
