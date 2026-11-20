package dev.fern.plex.rows;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.ContentResolver;
import android.content.Intent;
import android.database.Cursor;
import android.media.tv.TvContract;
import android.os.Bundle;

public final class Rows extends Instrumentation {
    @Override public void onCreate(Bundle args) {
        super.onCreate(args);
        start();
    }

    private static boolean opensItem(String uri, String id) throws Exception {
        if (uri == null) return false;
        Intent intent = Intent.parseUri(uri, Intent.URI_INTENT_SCHEME);
        return intent.getComponent() != null
            && "dev.fern.plex".equals(intent.getComponent().getPackageName())
            && id.equals(intent.getStringExtra("play"));
    }

    private long channel(ContentResolver resolver) {
        String[] columns = {"_id", "package_name", "display_name", "type"};
        try (Cursor c = resolver.query(TvContract.Channels.CONTENT_URI, columns, null, null, null)) {
            while (c.moveToNext()) {
                if ("dev.fern.plex".equals(c.getString(1)) && "Fern for Plex".equals(c.getString(2))
                        && TvContract.Channels.TYPE_PREVIEW.equals(c.getString(3))) {
                    return c.getLong(0);
                }
            }
        }
        throw new AssertionError("Plex has no preview channel");
    }

    private void previews(ContentResolver resolver, long channel) throws Exception {
        String[] columns = {"channel_id", "internal_provider_id", "title", "intent_uri"};
        boolean movie = false, track = false;
        try (Cursor c = resolver.query(TvContract.PreviewPrograms.CONTENT_URI, columns, null, null, null)) {
            while (c.moveToNext()) {
                if (c.getLong(0) != channel || !opensItem(c.getString(3), c.getString(1))) continue;
                movie |= "901".equals(c.getString(1)) && "Test Pattern".equals(c.getString(2));
                track |= "900".equals(c.getString(1)) && "Square Tone".equals(c.getString(2));
            }
        }
        if (!movie || !track) throw new AssertionError("missing or incorrect recent items on Plex channel");
    }

    private void watchNext(ContentResolver resolver) throws Exception {
        String[] columns = {"package_name", "internal_provider_id", "title", "intent_uri",
            "watch_next_type", "last_playback_position_millis", "duration_millis"};
        try (Cursor c = resolver.query(TvContract.WatchNextPrograms.CONTENT_URI, columns, null, null, null)) {
            while (c.moveToNext()) {
                if ("dev.fern.plex".equals(c.getString(0)) && "901".equals(c.getString(1))
                        && "Test Pattern".equals(c.getString(2)) && opensItem(c.getString(3), "901")
                        && c.getInt(4) == TvContract.WatchNextPrograms.WATCH_NEXT_TYPE_CONTINUE
                        && c.getLong(5) > 0 && c.getLong(5) < c.getLong(6)) return;
            }
        }
        throw new AssertionError("unfinished movie has no resumable Watch Next entry");
    }

    @Override public void onStart() {
        Bundle result = new Bundle();
        try {
            ContentResolver resolver = getTargetContext().getContentResolver();
            previews(resolver, channel(resolver));
            watchNext(resolver);
            result.putString("rows", "verified");
            finish(Activity.RESULT_OK, result);
        } catch (Exception | AssertionError failure) {
            result.putString("error", failure.toString());
            finish(Activity.RESULT_CANCELED, result);
        }
    }
}
