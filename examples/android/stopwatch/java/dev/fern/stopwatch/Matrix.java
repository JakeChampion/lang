package dev.fern.stopwatch;

import android.graphics.Canvas;
import android.graphics.Paint;

/** The dot-matrix font of matrix.fern: 5 by 7 round dots for each character. */
final class Matrix {
    static final int COLS = 5;
    static final int ROWS = 7;

    private static final long[] MASKS = new long[128];
    private static final boolean[] LOADED = new boolean[128];

    private Matrix() {}

    private static long mask(char c) {
        if (c >= MASKS.length) {
            return 0;
        }
        if (!LOADED[c]) {
            MASKS[c] = Core.glyphMask(c);
            LOADED[c] = true;
        }
        return MASKS[c];
    }

    /** The width of `s` with `pitch` pixels from dot to dot. A column of space separates two characters. */
    static float width(String s, float pitch) {
        return s.isEmpty() ? 0 : (s.length() * (COLS + 1) - 1) * pitch;
    }

    static float height(float pitch) {
        return ROWS * pitch;
    }

    /** Draws `s` with the top-left dot cell at x and y. A dot has the radius `dot` times the pitch. */
    static void draw(Canvas canvas, Paint paint, String s, float x, float y, float pitch, float dot, int color) {
        paint.setStyle(Paint.Style.FILL);
        paint.setColor(color);
        float r = Math.max(pitch * dot, 0.75f);
        for (int k = 0; k < s.length(); k++) {
            long m = mask(s.charAt(k));
            float left = x + k * (COLS + 1) * pitch;
            for (int row = 0; row < ROWS && m != 0; row++) {
                for (int col = 0; col < COLS; col++) {
                    if ((m >> (row * COLS + col) & 1) != 0) {
                        canvas.drawCircle(left + col * pitch + pitch / 2, y + row * pitch + pitch / 2, r, paint);
                    }
                }
            }
        }
    }
}
