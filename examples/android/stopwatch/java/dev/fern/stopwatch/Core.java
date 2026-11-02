package dev.fern.stopwatch;

/** The app logic, implemented in Fern (stopwatch.fern, exported by native.fern). */
final class Core {
    static {
        System.loadLibrary("fern");
    }

    private Core() {}

    static native long elapsedMs(long startedAt, long anteriority, long now);
    static native boolean isRunning(long startedAt);
    static native long start(long startedAt, long now);
    static native long pause(long startedAt, long anteriority, long now);

    static native String stopwatchText(long elapsedMs, boolean showSeconds);
    static native String clockText(long localMs);
    static native long stopwatchTickIn(long elapsedMs, boolean showSeconds);
    static native long clockTickIn(long localMs);

    static native int stopwatchColor(boolean running, int opacity);
    static native int clockColor(int opacity);
    static native boolean keepScreenOn(boolean running, boolean stopwatchEnabled);

    static native int buttonsHideDelayMs();
    static native int burnInPeriodMs();
    static native int burnInOrigin(int panel, int content, int reserved, int displacement, int random);
    static native long nextBeepIn(long elapsedMs, int intervalMinutes);
    static native String aboutText(long totalRuntimeMs);

    static native int settingCount();
    static native String settingKey(int i);
    static native String settingSection(int i);
    static native String settingTitle(int i);
    static native String settingSummary(int i);
    static native boolean settingIsToggle(int i);
    static native int settingMin(int i);
    static native int settingMax(int i);
    static native int settingDefault(int i);
    static native int settingClamp(int i, int value);
    static native String settingValueText(int i, int value);
}
