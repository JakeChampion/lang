package dev.fern.stopwatch;

import android.app.Activity;
import android.os.Bundle;
import android.view.MenuItem;
import android.widget.ScrollView;
import android.widget.TextView;

/** How to use the app, and the total runtime of all sessions. */
public final class AboutActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setTitle(R.string.about);
        if (getActionBar() != null) {
            getActionBar().setDisplayHomeAsUpEnabled(true);
        }
        TextView text = new TextView(this);
        text.setTextSize(17);
        text.setLineSpacing(0, 1.3f);
        int pad = Math.round(16 * getResources().getDisplayMetrics().density);
        text.setPadding(pad, pad, pad, pad);
        ScrollView scroll = new ScrollView(this);
        scroll.addView(text);
        setContentView(scroll);
        text.setText(Core.aboutText(Prefs.state(this).getLong(Prefs.TOTAL_RUNTIME, 0)));
    }

    @Override
    public boolean onOptionsItemSelected(MenuItem item) {
        if (item.getItemId() == android.R.id.home) {
            finish();
            return true;
        }
        return super.onOptionsItemSelected(item);
    }
}
