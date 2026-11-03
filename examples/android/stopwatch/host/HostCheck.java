package dev.fern.stopwatch;

/**
 * Calls every native method of Core in a desktop JVM, with the x86-64 build
 * of libfern.so on java.library.path. Prints "ok" and exits 0 when all match.
 */
public final class HostCheck {
    private static int failures = 0;

    private static void check(String what, Object got, Object want) {
        if (!got.equals(want)) {
            failures++;
            System.out.println("FAIL " + what + ": got " + got + ", want " + want);
        }
    }

    public static void main(String[] args) {
        check("elapsedMs paused", Core.elapsedMs(0, 4200, 99999), 4200L);
        check("elapsedMs running", Core.elapsedMs(1000, 500, 3500), 3000L);
        check("isRunning", Core.isRunning(0) + " " + Core.isRunning(1), "false true");
        long started = Core.start(0, 1_700_000_000_000L);
        check("start", started, 1_700_000_000_000L);
        check("start when running", Core.start(started, 1_700_000_005_000L), started);
        check("pause", Core.pause(started, 250, 1_700_000_003_000L), 3250L);

        check("stopwatchText", Core.stopwatchText(3_723_999L, true), "1:02:03");
        check("stopwatchText minutes", Core.stopwatchText(3_723_999L, false), "1:02");
        check("clockText", Core.clockText((9 * 3600 + 5 * 60) * 1000L), "09:05");
        check("stopwatchTickIn", Core.stopwatchTickIn(1999, true), 11L);
        check("stopwatchTickIn minutes", Core.stopwatchTickIn(61_000, false), 59_010L);
        check("clockTickIn", Core.clockTickIn(150_000), 30_010L);

        check("stopwatchColor running", Core.stopwatchColor(true, 20), 0xFFFFFFFF);
        check("stopwatchColor paused", Core.stopwatchColor(false, 20), 0xFFEE5555);
        check("clockColor", Core.clockColor(10), 0x7FFFFFFF);
        check("keepScreenOn", Core.keepScreenOn(true, true) + " " + Core.keepScreenOn(false, true) + " " + Core.keepScreenOn(false, false), "true false true");

        check("buttonsHideDelayMs", Core.buttonsHideDelayMs(), 5000);
        check("burnInPeriodMs", Core.burnInPeriodMs(), 60_000);
        check("burnInOffset", Core.burnInOffset(1000, 400, 50, 0, 99), 0);
        check("burnInOffset moved", Core.burnInOffset(1000, 400, 50, 10, 0), -250);
        check("nextBeepIn", Core.nextBeepIn(299_000, 5), 1000L);
        check("nextBeepIn off", Core.nextBeepIn(299_000, 0), -1L);
        check("aboutText", Core.aboutText(2 * 3_600_000L).contains("Total runtime: 2 hours"), true);

        int n = Core.settingCount();
        check("settingCount", n, 12);
        StringBuilder keys = new StringBuilder();
        for (int i = 0; i < n; i++) {
            keys.append(Core.settingKey(i)).append(i + 1 < n ? "," : "");
            if (Core.settingDefault(i) < Core.settingMin(i) || Core.settingDefault(i) > Core.settingMax(i)) {
                check("default in range " + i, false, true);
            }
        }
        check("settingKey", keys.toString(), "clock_enabled,clock_thin,clock_opacity,clock_size,stopwatch_enabled,stopwatch_seconds,stopwatch_thin,stopwatch_opacity,stopwatch_size,beep_interval,beep_override_silent,burn_in");
        check("settingSection", Core.settingSection(4), "Stopwatch");
        check("settingTitle", Core.settingTitle(11), "Screen burn-in protection");
        check("settingSummary", Core.settingSummary(0), "");
        check("settingIsToggle", Core.settingIsToggle(0) + " " + Core.settingIsToggle(2), "true false");
        check("settingClamp", Core.settingClamp(7, 99), 20);
        check("settingValueText", Core.settingValueText(9, 15), "15 min");

        // A long session: the text changes every second for many hours.
        long sum = 0;
        for (int i = 0; i < 200_000; i++) {
            sum += Core.stopwatchText(i * 1000L, true).length();
        }
        check("stopwatchText loop", sum > 0, true);

        if (failures != 0) {
            System.exit(1);
        }
        System.out.println("ok");
    }
}
