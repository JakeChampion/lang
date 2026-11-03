package dev.fern.stopwatch;

import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.graphics.Color;
import android.graphics.Typeface;
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
import android.widget.ImageButton;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.util.Random;
import java.util.TimeZone;

/**
 * The full-screen black display: a clock above a stopwatch. Tap the
 * stopwatch to start or pause it, long-tap it to reset it, and tap anywhere
 * to show the menu button. Core makes every decision; this class only
 * shows the result and schedules the next update.
 */
public final class MainActivity extends Activity {
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Random random = new Random();

    private FrameLayout root;
    private LinearLayout content;
    private TextView clock;
    private TextView stopwatch;
    private ImageButton menuButton;
    private LinearLayout menu;
    private ToneGenerator tone;

    private long startedAt;
    private long anteriority;

    private boolean clockEnabled;
    private boolean stopwatchEnabled;
    private boolean showSeconds;
    private int stopwatchOpacity;
    private int displacement;
    private int beepInterval;

    private final Runnable stopwatchTick = new Runnable() {
        @Override
        public void run() {
            long elapsed = elapsed();
            stopwatch.setText(Core.stopwatchText(elapsed, showSeconds));
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

        clock = new TextView(this);
        clock.setPadding(dp(30), dp(8), dp(30), dp(8));
        clock.setIncludeFontPadding(false);
        content.addView(clock, wrap());

        stopwatch = new TextView(this);
        stopwatch.setPadding(dp(30), dp(15), dp(30), dp(15));
        stopwatch.setIncludeFontPadding(false);
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
        content.addView(stopwatch, wrap());

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

        menuButton = new ImageButton(this);
        menuButton.setImageResource(R.drawable.ic_menu);
        menuButton.setColorFilter(0xFFABABB1);
        menuButton.setBackgroundColor(Color.TRANSPARENT);
        menuButton.setScaleType(ImageView.ScaleType.CENTER);
        menuButton.setContentDescription(getString(R.string.open_menu));
        menuButton.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                showMenuButton();
                menu.setVisibility(menu.getVisibility() == View.VISIBLE ? View.GONE : View.VISIBLE);
            }
        });
        FrameLayout.LayoutParams menuParams = new FrameLayout.LayoutParams(
                bar(), bar(), Gravity.BOTTOM | Gravity.END);
        menuParams.setMargins(0, 0, dp(16), 0);
        root.addView(menuButton, menuParams);

        menu = new LinearLayout(this);
        menu.setOrientation(LinearLayout.VERTICAL);
        menu.setBackgroundColor(0xFF1C1C1E);
        menu.setPadding(0, dp(8), 0, dp(8));
        menu.setVisibility(View.GONE);
        menu.addView(menuItem(R.string.settings, SettingsActivity.class));
        menu.addView(menuItem(R.string.about, AboutActivity.class));
        FrameLayout.LayoutParams panelParams = new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT, FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.BOTTOM | Gravity.END);
        panelParams.setMargins(0, 0, dp(16), bar());
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

        style(clock, Prefs.on(this, "clock_thin"), Prefs.get(this, "clock_size"));
        clock.setTextColor(Core.clockColor(Prefs.get(this, "clock_opacity")));
        clock.setVisibility(clockEnabled ? View.VISIBLE : View.GONE);
        style(stopwatch, Prefs.on(this, "stopwatch_thin"), Prefs.get(this, "stopwatch_size"));
        stopwatch.setVisibility(stopwatchEnabled ? View.VISIBLE : View.GONE);

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
        stopwatch.setTextColor(Core.stopwatchColor(running, stopwatchOpacity));
        stopwatch.setText(Core.stopwatchText(elapsed(), showSeconds));
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
        menuButton.setVisibility(View.VISIBLE);
        handler.postDelayed(hideMenuButton, Core.buttonsHideDelayMs());
    }

    private TextView menuItem(int label, final Class<? extends Activity> target) {
        TextView item = new TextView(this);
        item.setText(label);
        item.setTextColor(0xFFDDDDDD);
        item.setTextSize(18);
        item.setPadding(dp(24), dp(14), dp(48), dp(14));
        item.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                startActivity(new Intent(MainActivity.this, target));
            }
        });
        return item;
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

    private static void style(TextView view, boolean thin, int sizeSp) {
        view.setTypeface(Typeface.create(thin ? "sans-serif-thin" : "sans-serif", Typeface.NORMAL));
        view.setTextSize(TypedValue.COMPLEX_UNIT_SP, sizeSp);
    }

    private static LinearLayout.LayoutParams wrap() {
        return new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
    }

    private int bar() {
        return dp(56);
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }
}
