package dev.fern.stopwatch;

import android.content.Context;
import android.content.SharedPreferences;

import java.util.HashMap;
import java.util.Map;

/** Stored settings and stopwatch state. The settings table comes from Core. */
final class Prefs {
    static final String STARTED_AT = "started_at";
    static final String ANTERIORITY = "anteriority";
    static final String TOTAL_RUNTIME = "total_runtime";

    private static Map<String, Integer> index;

    private Prefs() {}

    static SharedPreferences settings(Context context) {
        return context.getSharedPreferences("settings", Context.MODE_PRIVATE);
    }

    static SharedPreferences state(Context context) {
        return context.getSharedPreferences("state", Context.MODE_PRIVATE);
    }

    static synchronized int indexOf(String key) {
        if (index == null) {
            index = new HashMap<>();
            for (int i = 0; i < Core.settingCount(); i++) {
                index.put(Core.settingKey(i), i);
            }
        }
        Integer i = index.get(key);
        if (i == null) {
            throw new IllegalArgumentException("no setting " + key);
        }
        return i;
    }

    static int get(Context context, String key) {
        int i = indexOf(key);
        return Core.settingClamp(i, settings(context).getInt(key, Core.settingDefault(i)));
    }

    static boolean on(Context context, String key) {
        return get(context, key) != 0;
    }

    static void set(Context context, String key, int value) {
        settings(context).edit().putInt(key, Core.settingClamp(indexOf(key), value)).apply();
    }
}
