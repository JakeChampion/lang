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


if __name__ == "__main__":
    unittest.main()
