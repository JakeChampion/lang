package dev.fern.stopwatch;

import android.app.Activity;
import android.os.Bundle;
import android.view.Gravity;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/**
 * How to use the app, and the total runtime of all sessions. Each paragraph
 * of the about text of Core with a heading is a card. A line "name: value"
 * shows the name in white and the value in grey.
 */
public final class AboutActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        LinearLayout list = new LinearLayout(this);
        list.setOrientation(LinearLayout.VERTICAL);
        list.setPadding(dp(20), 0, dp(20), dp(32));
        list.addView(Theme.header(this, getString(R.string.about)));
        String about = Core.aboutText(Prefs.state(this).getLong(Prefs.TOTAL_RUNTIME, 0));
        for (String paragraph : about.split("\n\n")) {
            String[] lines = paragraph.split("\n");
            if (lines.length == 1) {
                TextView note = Theme.text(this, lines[0], 12, Theme.MUTED);
                note.setPadding(dp(4), dp(24), dp(4), 0);
                list.addView(note);
                continue;
            }
            list.addView(Theme.section(this, lines[0]));
            LinearLayout card = new LinearLayout(this);
            card.setOrientation(LinearLayout.VERTICAL);
            card.setBackground(Theme.card(this, Theme.SURFACE));
            card.setPadding(dp(18), dp(8), dp(18), dp(8));
            for (int k = 1; k < lines.length; k++) {
                card.addView(line(lines[k]));
            }
            list.addView(card);
        }
        ScrollView scroll = new ScrollView(this);
        scroll.setBackgroundColor(Theme.BLACK);
        scroll.addView(list);
        setContentView(scroll);
    }

    private LinearLayout line(String text) {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(0, dp(10), 0, dp(10));
        int split = text.indexOf(": ");
        String name = split < 0 ? text : text.substring(0, split);
        row.addView(Theme.text(this, name, 15, Theme.INK),
                new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1));
        if (split >= 0) {
            row.addView(Theme.text(this, text.substring(split + 2), 15, Theme.MUTED));
        }
        return row;
    }

    private int dp(float value) {
        return Theme.dp(this, value);
    }
}
