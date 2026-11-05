package dev.fern.stopwatch;

import android.app.Activity;
import android.content.Context;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** The palette of Core and the shared parts of the screens: cards, text and the header. */
final class Theme {
    static final int BLACK = 0xFF000000;
    static final int INK = Core.ink();
    static final int MUTED = Core.muted();
    static final int OUTLINE = Core.outline();
    static final int SURFACE = Core.surface();
    static final int RED = Core.red();

    /** The radius of a dot as a part of the pitch, for the normal and the thin font. */
    static final float DOT = 0.4f;
    static final float THIN_DOT = 0.26f;

    private Theme() {}

    static int dp(Context context, float value) {
        return Math.round(value * context.getResources().getDisplayMetrics().density);
    }

    /** `rgb` with the alpha of `argb`. */
    static int withAlphaOf(int argb, int rgb) {
        return (argb & 0xFF000000) | (rgb & 0x00FFFFFF);
    }

    /** A rounded rectangle of the surface, with an outline in `edge`. */
    static GradientDrawable card(Context context, int edge) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(SURFACE);
        d.setCornerRadius(dp(context, 18));
        d.setStroke(Math.max(1, dp(context, 1.5f)), edge);
        return d;
    }

    static TextView text(Context context, String s, float sp, int color) {
        TextView view = new TextView(context);
        view.setText(s);
        view.setTextSize(sp);
        view.setTextColor(color);
        view.setTypeface(Typeface.MONOSPACE);
        view.setIncludeFontPadding(false);
        view.setLineSpacing(0, 1.25f);
        return view;
    }

    /** The title of a screen in dots, and an outlined BACK pill that closes it. */
    static View header(final Activity activity, String title) {
        LinearLayout row = new LinearLayout(activity);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(0, dp(activity, 28), 0, dp(activity, 12));
        row.addView(new DotText(activity, title, dp(activity, 4), INK));
        row.addView(new View(activity), new LinearLayout.LayoutParams(0, 1, 1));
        Pill back = new Pill(activity, "Back", Pill.OUTLINED, MUTED);
        back.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                activity.finish();
            }
        });
        row.addView(back);
        return row;
    }

    /** A section name in small grey dots. */
    static View section(Context context, String name) {
        DotText view = new DotText(context, name, dp(context, 2.2f), MUTED);
        view.setPadding(dp(context, 4), dp(context, 24), 0, dp(context, 10));
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        view.setLayoutParams(params);
        return view;
    }
}
