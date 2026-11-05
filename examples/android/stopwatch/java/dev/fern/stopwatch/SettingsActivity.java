package dev.fern.stopwatch;

import android.app.Activity;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

/** One card for each entry of the settings table in Core, under the name of its section. */
public final class SettingsActivity extends Activity {
    private LinearLayout list;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        list = new LinearLayout(this);
        list.setOrientation(LinearLayout.VERTICAL);
        list.setPadding(dp(20), 0, dp(20), dp(32));
        list.addView(Theme.header(this, getString(R.string.settings)));
        String section = "";
        for (int i = 0; i < Core.settingCount(); i++) {
            if (!Core.settingSection(i).equals(section)) {
                section = Core.settingSection(i);
                list.addView(Theme.section(this, section));
            }
            View row = Core.settingIsToggle(i) ? toggleRow(i) : sliderRow(i);
            LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
            params.setMargins(0, 0, 0, dp(8));
            list.addView(row, params);
        }
        ScrollView scroll = new ScrollView(this);
        scroll.setBackgroundColor(Theme.BLACK);
        scroll.addView(list);
        setContentView(scroll);
    }

    private LinearLayout card(int orientation) {
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(orientation);
        row.setBackground(Theme.card(this, Theme.SURFACE));
        row.setPadding(dp(18), dp(16), dp(18), dp(16));
        return row;
    }

    private View toggleRow(final int i) {
        final String key = Core.settingKey(i);
        LinearLayout row = card(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.addView(labels(i, null), new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1));
        final DotSwitch toggle = new DotSwitch(this, Prefs.on(this, key));
        toggle.setListener(new DotSwitch.Listener() {
            @Override
            public void onChange(boolean checked) {
                Prefs.set(SettingsActivity.this, key, checked ? 1 : 0);
            }
        });
        row.addView(toggle);
        row.setOnClickListener(new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                toggle.toggle();
            }
        });
        return row;
    }

    private View sliderRow(final int i) {
        final String key = Core.settingKey(i);
        int value = Prefs.get(this, key);
        LinearLayout row = card(LinearLayout.VERTICAL);
        final DotText valueLabel = new DotText(this, Core.settingValueText(i, value), dp(2.4f), Theme.INK);
        row.addView(labels(i, valueLabel));
        DotSlider bar = new DotSlider(this, Core.settingMin(i), Core.settingMax(i), value);
        bar.setContentDescription(Core.settingTitle(i));
        bar.setListener(new DotSlider.Listener() {
            @Override
            public void onChange(int v) {
                Prefs.set(SettingsActivity.this, key, v);
                valueLabel.setText(Core.settingValueText(i, v));
            }
        });
        LinearLayout.LayoutParams params = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        params.setMargins(0, dp(10), 0, 0);
        row.addView(bar, params);
        return row;
    }

    /** The title, with the value in dots at the end of the line, and the summary below. */
    private View labels(int i, DotText valueLabel) {
        LinearLayout column = new LinearLayout(this);
        column.setOrientation(LinearLayout.VERTICAL);
        LinearLayout line = new LinearLayout(this);
        line.setOrientation(LinearLayout.HORIZONTAL);
        line.setGravity(Gravity.CENTER_VERTICAL);
        TextView title = Theme.text(this, Core.settingTitle(i), 15, Theme.INK);
        line.addView(title, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1));
        if (valueLabel != null) {
            line.addView(valueLabel);
        }
        column.addView(line);
        String summary = Core.settingSummary(i);
        if (!summary.isEmpty()) {
            TextView view = Theme.text(this, summary, 12, Theme.MUTED);
            view.setPadding(0, dp(6), dp(8), 0);
            column.addView(view);
        }
        return column;
    }

    private int dp(float value) {
        return Theme.dp(this, value);
    }
}
