import unittest

import emulator_check

LISTING = """
  NotificationRecord(0x1: pkg=com.other user=UserHandle{0} id=1 tag=null)
    extras={ android.title=Other }
  NotificationRecord(0x2: pkg=dev.fern.notifytest user=UserHandle{0} id=7 tag=null)
    extras={ android.title=Fern notify test android.text=half done android.progress=50 android.progressMax=100 }
"""


class RecordsTest(unittest.TestCase):
    def test_only_the_records_of_the_package(self):
        records = emulator_check.records(LISTING)
        self.assertEqual(len(records), 1)
        self.assertIn("id=7", records[0])

    def test_a_shown_notification_passes(self):
        self.assertEqual(emulator_check.check_shown(LISTING), [])

    def test_a_missing_progress_fails(self):
        failures = emulator_check.check_shown(LISTING.replace("android.progress=50", "android.progress=5"))
        self.assertEqual(failures, ["the notification has no progress of 50"])

    def test_no_record_fails(self):
        failures = emulator_check.check_shown(LISTING.replace("dev.fern.notifytest", "dev.fern.other"))
        self.assertEqual(len(failures), 1)
        self.assertIn("no notification", failures[0])


class WaitShownTest(unittest.TestCase):
    def listings(self, *listings):
        left = list(listings)
        return lambda: left.pop(0) if len(left) > 1 else left[0]

    def test_a_stale_listing_is_read_again(self):
        stale = LISTING.replace(" android.progress=50 android.progressMax=100", "")
        listing, failures = emulator_check.wait_shown(self.listings(stale, LISTING), 3)
        self.assertEqual(failures, [])
        self.assertEqual(listing, LISTING)

    def test_a_listing_that_stays_stale_fails(self):
        stale = LISTING.replace(" android.progress=50 android.progressMax=100", "")
        listing, failures = emulator_check.wait_shown(self.listings(stale), 0)
        self.assertEqual(failures, ["the notification has no progress of 50"])
        self.assertEqual(listing, stale)


if __name__ == "__main__":
    unittest.main()
