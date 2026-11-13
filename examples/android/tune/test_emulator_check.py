import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location("tune_emulator_check", Path(__file__).with_name("emulator_check.py"))
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class WidgetProviderTests(unittest.TestCase):
    def test_android_component_info(self):
        dump = "[0] provider ProviderId{user:0, app:10123, cmp:ComponentInfo{dev.fern.tune/dev.fern.tune.Widget}}"
        self.assertTrue(check.has_widget_provider(dump))

    def test_short_component(self):
        self.assertTrue(check.has_widget_provider("provider dev.fern.tune/.Widget\n"))

    def test_other_components_do_not_match(self):
        for dump in ["", "dev.fern.other/.Widget", "dev.fern.tune/.WidgetOther",
                     "dev.fern.tune/dev.fern.tune.Widget$Other", "dev.fern.tune/.Widget.Other",
                     "otherdev.fern.tune/.Widget", "dev.fern.tune/dev.fern.other.Widget"]:
            with self.subTest(dump=dump):
                self.assertFalse(check.has_widget_provider(dump))


class TaskTests(unittest.TestCase):
    def test_task_of_the_app(self):
        stacks = ("RootTask id=1 bounds=[0,0][1080,2400] displayId=0 userId=0\n"
                  "  taskId=1: com.android.launcher3/.uioverrides.QuickstepLauncher bounds=[0,0][1080,2400] userId=0 visible=false\n"
                  "RootTask id=34 bounds=[0,0][1080,2400] displayId=0 userId=0\n"
                  "  taskId=34: dev.fern.tune/dev.fern.tune.Main bounds=[0,0][1080,2400] userId=0 visible=true\n")
        self.assertEqual(check.task_of(stacks), "34")

    def test_no_task(self):
        for stacks in ["", "  taskId=7: dev.fern.tuner/.Main", "  taskId=7: com.android.launcher3/.Launcher"]:
            with self.subTest(stacks=stacks):
                self.assertIsNone(check.task_of(stacks))


if __name__ == "__main__":
    unittest.main()
