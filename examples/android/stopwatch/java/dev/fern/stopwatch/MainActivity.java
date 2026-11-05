package dev.fern.stopwatch;

import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.graphics.Color;
import android.graphics.drawable.ColorDrawable;
import android.media.AudioManager;
import android.media.ToneGenerator;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.WindowManager;
import android.widget.FrameLayout;
import android.widget.LinearLayout;

import java.util.Random;
import java.util.TimeZone;

/**
 * The full-screen black display: a clock in dots above the dial of the
 * stopwatch. Tap the dial to start or pause it, long-tap it to reset it, and
 * tap anywhere to show the title and the menu button. Core makes every
 * decision; this class only shows the result and schedules the next update.
 */
public final class MainActivity extends Activity {
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Random random = new Random();

    private FrameLayout root;
    private LinearLayout content;
    private DotText clock;
    private Dial stopwatch;
    private DotText title;
    private Pill menuButton;
    private LinearLayout menu;
    private ToneGenerator tone;

    private long startedAt;
    private long anteriority;

    private boolean clockEnabled;
    private boolean stopwatchEnabled;
    private boolean showSeconds;
    private int stopwatchOpacity;
    private int[] ring;
    private int displacement;
    private int beepInterval;

    private final Runnable stopwatchTick = new Runnable() {
        @Override
        public void run() {
            long elapsed = elapsed();
            show(elapsed);
            handler.postDelayed(this, Core.stopwatchTickIn(elapsed, showSeconds));
        }
    };

    private final Runnable clockTick = new Runnable() {
        @Override
        public void run() {
            long local = localNow();
            clock.setText(Core.clockText(local));
            handler.postDelayed(this, Core.clockTickIn(local));
        }
    };

    private final Runnable beepTick = new Runnable() {
        @Override
        public void run() {
            if (tone != null) {
                tone.startTone(ToneGenerator.TONE_PROP_BEEP, 200);
            }
            scheduleBeep();
        }
    };

    private final Runnable burnInTick = new Runnable() {
        @Override
        public void run() {
            place();
            handler.postDelayed(this, Core.burnInPeriodMs());
        }
    };

    private final Runnable hideMenuButton = new Runnable() {
        @Override
        public void run() {
            title.setVisibility(View.GONE);
            menuButton.setVisibility(View.GONE);
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().setBackgroundDrawable(new ColorDrawable(Color.BLACK));

        root = new FrameLayout(this);
        root.setBackgroundColor(Color.BLACK);
        root.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                menu.setVisibility(View.GONE);
                showMenuButton();
            }
        });

        content = new LinearLayout(this);
        content.setOrientation(LinearLayout.VERTICAL);
        content.setGravity(Gravity.CENTER_HORIZONTAL);

        clock = new DotText(this, "", 1, Theme.INK);
        clock.setPadding(dp(30), dp(8), dp(30), dp(8));
        content.addView(clock, wrap());

        stopwatch = new Dial(this);
        stopwatch.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                toggle();
            }
        });
        stopwatch.setOnLongClickListener(new View.OnLongClickListener() {
            @Override
            public boolean onLongClick(View v) {
                reset();
                return true;
            }
        });
        LinearLayout.LayoutParams dialParams = wrap();
        dialParams.setMargins(0, dp(12), 0, dp(12));
        content.addView(stopwatch, dialParams);

        root.addView(content, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT, FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));
        content.addOnLayoutChangeListener(new View.OnLayoutChangeListener() {
            @Override
            public void onLayoutChange(View v, int l, int t, int r, int b, int ol, int ot, int or, int ob) {
                if (r - l != or - ol || b - t != ob - ot) {
                    place();
                }
            }
        });
        root.addOnLayoutChangeListener(new View.OnLayoutChangeListener() {
            @Override
            public void onLayoutChange(View v, int l, int t, int r, int b, int ol, int ot, int or, int ob) {
                if (r - l != or - ol || b - t != ob - ot) {
                    place();
                }
            }
        });

        title = new DotText(this, "Stopwatch", dp(3.2f), Theme.INK);
        FrameLayout.LayoutParams titleParams = new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT, dp(44), Gravity.TOP | Gravity.START);
        titleParams.setMargins(dp(24), dp(28), 0, 0);
        root.addView(title, titleParams);

        menuButton = new Pill(this, "Menu", Pill.OUTLINED, Theme.MUTED);
        menuButton.setContentDescription(getString(R.string.open_menu));
        menuButton.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                showMenuButton();
                menu.setVisibility(menu.getVisibility() == View.VISIBLE ? View.GONE : View.VISIBLE);
            }
        });
        FrameLayout.LayoutParams menuParams = new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT, FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.TOP | Gravity.END);
        menuParams.setMargins(0, dp(28), dp(24), 0);
        root.addView(menuButton, menuParams);

        menu = new LinearLayout(this);
        menu.setOrientation(LinearLayout.VERTICAL);
        menu.setBackground(Theme.card(this, Theme.OUTLINE));
        menu.setPadding(dp(12), dp(12), dp(12), dp(4));
        menu.setVisibility(View.GONE);
        menu.addView(menuItem(R.string.settings, SettingsActivity.class), menuItemParams());
        menu.addView(menuItem(R.string.about, AboutActivity.class), menuItemParams());
        FrameLayout.LayoutParams panelParams = new FrameLayout.LayoutParams(
                dp(200), FrameLayout.LayoutParams.WRAP_CONTENT, Gravity.TOP | Gravity.END);
        panelParams.setMargins(0, dp(28 + 44 + 12), dp(24), 0);
        root.addView(menu, panelParams);

        setContentView(root);
    }

    @Override
    protected void onStart() {
        super.onStart();
        hideSystemBars();

        SharedPreferences state = Prefs.state(this);
        startedAt = state.getLong(Prefs.STARTED_AT, 0);
        anteriority = state.getLong(Prefs.ANTERIORITY, 0);

        clockEnabled = Prefs.on(this, "clock_enabled");
        stopwatchEnabled = Prefs.on(this, "stopwatch_enabled");
        showSeconds = Prefs.on(this, "stopwatch_seconds");
        stopwatchOpacity = Prefs.get(this, "stopwatch_opacity");
        displacement = Prefs.get(this, "burn_in");
        beepInterval = Prefs.get(this, "beep_interval");

        clock.setThin(Prefs.on(this, "clock_thin"));
        clock.setPitch(sp(Prefs.get(this, "clock_size")) / 10);
        clock.setColor(Core.clockColor(Prefs.get(this, "clock_opacity")));
        clock.setVisibility(clockEnabled ? View.VISIBLE : View.GONE);
        stopwatch.setThin(Prefs.on(this, "stopwatch_thin"));
        stopwatch.setSize(sp(Prefs.get(this, "stopwatch_size")));
        stopwatch.setVisibility(stopwatchEnabled ? View.VISIBLE : View.GONE);
        ring = new int[Core.ringDots()];

        int stream = Prefs.on(this, "beep_override_silent") ? AudioManager.STREAM_ALARM : AudioManager.STREAM_NOTIFICATION;
        setVolumeControlStream(stream);
        try {
            tone = new ToneGenerator(stream, 80);
        } catch (RuntimeException e) {
            tone = null;
        }

        if (!stopwatchEnabled && Core.isRunning(startedAt)) {
            pause();
        }
        if (clockEnabled) {
            clockTick.run();
        }
        if (displacement > 0) {
            handler.postDelayed(burnInTick, Core.burnInPeriodMs());
        }
        render();
        place();
        showMenuButton();
    }

    @Override
    protected void onStop() {
        super.onStop();
        menu.setVisibility(View.GONE);
        handler.removeCallbacksAndMessages(null);
        if (tone != null) {
            tone.release();
            tone = null;
        }
        setVolumeControlStream(AudioManager.USE_DEFAULT_STREAM_TYPE);
        getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
    }

    @Override
    public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) {
            hideSystemBars();
        }
    }

    private void toggle() {
        if (Core.isRunning(startedAt)) {
            pause();
        } else {
            startedAt = Core.start(startedAt, System.currentTimeMillis());
            save();
        }
        render();
    }

    private void pause() {
        anteriority = Core.pause(startedAt, anteriority, System.currentTimeMillis());
        startedAt = 0;
        save();
    }

    private void reset() {
        SharedPreferences state = Prefs.state(this);
        long total = state.getLong(Prefs.TOTAL_RUNTIME, 0) + elapsed();
        state.edit().putLong(Prefs.TOTAL_RUNTIME, total).apply();
        startedAt = 0;
        anteriority = 0;
        save();
        render();
    }

    private void save() {
        Prefs.state(this).edit()
                .putLong(Prefs.STARTED_AT, startedAt)
                .putLong(Prefs.ANTERIORITY, anteriority)
                .apply();
    }

    /** Shows the stopwatch state and schedules its next updates. */
    private void render() {
        boolean running = Core.isRunning(startedAt);
        handler.removeCallbacks(stopwatchTick);
        handler.removeCallbacks(beepTick);
        show(elapsed());
        if (running && stopwatchEnabled) {
            handler.postDelayed(stopwatchTick, Core.stopwatchTickIn(elapsed(), showSeconds));
            scheduleBeep();
        }
        if (Core.keepScreenOn(running, stopwatchEnabled)) {
            getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        } else {
            getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        }
    }

    /** Draws the dial for `elapsed`: the time, the state and the ring. */
    private void show(long elapsed) {
        boolean running = Core.isRunning(startedAt);
        for (int k = 0; k < ring.length; k++) {
            ring[k] = Core.ringColor(k, elapsed, showSeconds, running, stopwatchOpacity);
        }
        int color = Core.stopwatchColor(running, elapsed, stopwatchOpacity);
        stopwatch.show(Core.stopwatchText(elapsed, showSeconds), color,
                Core.statusText(running, elapsed), Theme.withAlphaOf(color, Theme.MUTED), ring);
    }

    private void scheduleBeep() {
        long delay = Core.nextBeepIn(elapsed(), beepInterval);
        if (delay >= 0) {
            handler.postDelayed(beepTick, delay);
        }
    }

    /**
     * Moves the centred content by the offset Core picks for burn-in
     * protection. A translation needs no second layout pass, so a call from a
     * layout listener shows in the frame that the layout is for.
     */
    private void place() {
        if (root.getWidth() == 0 || content.getWidth() == 0) {
            return;
        }
        boolean portrait = getResources().getConfiguration().orientation != Configuration.ORIENTATION_LANDSCAPE;
        content.setTranslationX(Core.burnInOffset(root.getWidth(), content.getWidth(), portrait ? 0 : bar(),
                displacement, random.nextInt(Integer.MAX_VALUE)));
        content.setTranslationY(Core.burnInOffset(root.getHeight(), content.getHeight(), portrait ? bar() : 0,
                displacement, random.nextInt(Integer.MAX_VALUE)));
    }

    private void showMenuButton() {
        handler.removeCallbacks(hideMenuButton);
        title.setVisibility(View.VISIBLE);
        menuButton.setVisibility(View.VISIBLE);
        handler.postDelayed(hideMenuButton, Core.buttonsHideDelayMs());
    }

    private Pill menuItem(int label, final Class<? extends Activity> target) {
        Pill item = new Pill(this, getString(label), Pill.OUTLINED, Theme.INK);
        item.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                startActivity(new Intent(MainActivity.this, target));
            }
        });
        return item;
    }

    private LinearLayout.LayoutParams menuItemParams() {
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        params.setMargins(0, 0, 0, dp(8));
        return params;
    }

    @SuppressWarnings("deprecation")
    private void hideSystemBars() {
        getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                        | View.SYSTEM_UI_FLAG_FULLSCREEN
                        | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                        | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                        | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION);
    }

    private long elapsed() {
        return Core.elapsedMs(startedAt, anteriority, System.currentTimeMillis());
    }

    private static long localNow() {
        long now = System.currentTimeMillis();
        return now + TimeZone.getDefault().getOffset(now);
    }

    private float sp(int value) {
        return TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, value, getResources().getDisplayMetrics());
    }

    private static LinearLayout.LayoutParams wrap() {
        return new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
    }

    /** The band at each end that the title and the menu button use. */
    private int bar() {
        return dp(28 + 44);
    }

    private int dp(float value) {
        return Theme.dp(this, value);
    }
}
