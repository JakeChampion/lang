package dev.fern.stopwatch;

import android.app.Activity;
import android.os.Bundle;
import android.view.MenuItem;
import android.view.View;
import android.widget.CompoundButton;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.SeekBar;
import android.widget.Switch;
import android.widget.TextView;

/** One row for each entry of the settings table in Core. */
public final class SettingsActivity extends Activity {
    private static final int ACCENT = 0xFFEE5555;

    private LinearLayout list;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setTitle(R.string.settings);
        if (getActionBar() != null) {
            getActionBar().setDisplayHomeAsUpEnabled(true);
        }
        list = new LinearLayout(this);
        list.setOrientation(LinearLayout.VERTICAL);
        list.setPadding(0, 0, 0, dp(24));
        String section = "";
        for (int i = 0; i < Core.settingCount(); i++) {
            if (!Core.settingSection(i).equals(section)) {
                section = Core.settingSection(i);
                list.addView(header(section));
            }
            list.addView(Core.settingIsToggle(i) ? toggleRow(i) : sliderRow(i));
        }
        ScrollView scroll = new ScrollView(this);
        scroll.addView(list);
        setContentView(scroll);
    }

    @Override
    public boolean onOptionsItemSelected(MenuItem item) {
        if (item.getItemId() == android.R.id.home) {
            finish();
            return true;
        }
        return super.onOptionsItemSelected(item);
    }

    private TextView header(String text) {
        TextView view = new TextView(this);
        view.setText(text);
        view.setTextColor(ACCENT);
        view.setTextSize(14);
        view.setAllCaps(true);
        view.setPadding(dp(16), dp(24), dp(16), dp(8));
        return view;
    }

    private View toggleRow(final int i) {
        final String key = Core.settingKey(i);
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setPadding(dp(16), dp(12), dp(16), dp(12));
        row.addView(labels(i, null), new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1));
        final Switch toggle = new Switch(this);
        toggle.setChecked(Prefs.on(this, key));
        toggle.setOnCheckedChangeListener(new CompoundButton.OnCheckedChangeListener() {
            @Override
            public void onCheckedChanged(CompoundButton button, boolean checked) {
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
        final int min = Core.settingMin(i);
        int value = Prefs.get(this, key);
        LinearLayout row = new LinearLayout(this);
        row.setOrientation(LinearLayout.VERTICAL);
        row.setPadding(dp(16), dp(12), dp(16), dp(4));
        final TextView valueLabel = new TextView(this);
        valueLabel.setText(Core.settingValueText(i, value));
        row.addView(labels(i, valueLabel));
        SeekBar bar = new SeekBar(this);
        bar.setMax(Core.settingMax(i) - min);
        bar.setProgress(value - min);
        bar.setOnSeekBarChangeListener(new SeekBar.OnSeekBarChangeListener() {
            @Override
            public void onProgressChanged(SeekBar seekBar, int progress, boolean fromUser) {
                Prefs.set(SettingsActivity.this, key, progress + min);
                valueLabel.setText(Core.settingValueText(i, progress + min));
            }

            @Override
            public void onStartTrackingTouch(SeekBar seekBar) {}

            @Override
            public void onStopTrackingTouch(SeekBar seekBar) {}
        });
        row.addView(bar);
        return row;
    }

    /** The title, with the value at the end of the line, and the summary below. */
    private View labels(int i, TextView valueLabel) {
        LinearLayout column = new LinearLayout(this);
        column.setOrientation(LinearLayout.VERTICAL);
        LinearLayout line = new LinearLayout(this);
        line.setOrientation(LinearLayout.HORIZONTAL);
        TextView title = new TextView(this);
        title.setText(Core.settingTitle(i));
        title.setTextSize(17);
        line.addView(title, new LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1));
        if (valueLabel != null) {
            valueLabel.setTextSize(15);
            line.addView(valueLabel);
        }
        column.addView(line);
        String summary = Core.settingSummary(i);
        if (!summary.isEmpty()) {
            TextView view = new TextView(this);
            view.setText(summary);
            view.setTextSize(13);
            view.setAlpha(0.7f);
            column.addView(view);
        }
        return column;
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }
}
