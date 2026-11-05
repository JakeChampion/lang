package dev.fern.stopwatch;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;

/**
 * The stopwatch: a dark disc in a ring of dots, with the time in large dots
 * and the state in small dots below it. Core gives the text and every colour.
 */
final class Dial extends View {
    private static final String TEMPLATE = "0:00:00";

    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private String text = TEMPLATE;
    private String status = "";
    private int textColor = Theme.INK;
    private int statusColor = Theme.MUTED;
    private int[] ring = new int[0];
    private float pitch;
    private float dot = Theme.DOT;

    Dial(Context context) {
        super(context);
        setClickable(true);
        setLongClickable(true);
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_YES);
    }

    /** The size of the digits, as a text size in pixels. */
    void setSize(float px) {
        pitch = px / 10;
        requestLayout();
    }

    void setThin(boolean thin) {
        dot = thin ? Theme.THIN_DOT : Theme.DOT;
        invalidate();
    }

    void show(String text, int textColor, String status, int statusColor, int[] ring) {
        this.text = text;
        this.textColor = textColor;
        this.status = status;
        this.statusColor = statusColor;
        this.ring = ring;
        invalidate();
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
        float want = Matrix.width(TEMPLATE, pitch) / 0.62f;
        float cap = Float.MAX_VALUE;
        if (MeasureSpec.getMode(widthSpec) != MeasureSpec.UNSPECIFIED) {
            cap = Math.min(cap, MeasureSpec.getSize(widthSpec) * 0.88f);
        }
        if (MeasureSpec.getMode(heightSpec) != MeasureSpec.UNSPECIFIED) {
            cap = Math.min(cap, MeasureSpec.getSize(heightSpec) * 0.92f);
        }
        int d = Math.round(Math.min(want, cap));
        setMeasuredDimension(d, d);
    }

    @Override
    protected void onDraw(Canvas canvas) {
        float cx = getWidth() / 2f;
        float cy = getHeight() / 2f;
        float rad = Math.min(cx, cy);
        float dotR = rad / 40 + 1;
        paint.setStyle(Paint.Style.FILL);
        paint.setColor(Theme.SURFACE);
        canvas.drawCircle(cx, cy, rad * 0.9f, paint);
        for (int k = 0; k < ring.length; k++) {
            double a = 2 * Math.PI * k / ring.length;
            paint.setColor(ring[k]);
            canvas.drawCircle(cx + (float) ((rad - dotR) * Math.sin(a)),
                    cy - (float) ((rad - dotR) * Math.cos(a)), dotR, paint);
        }
        float p = Math.min(pitch, rad * 1.3f / ((text.length() * (Matrix.COLS + 1) - 1)));
        float th = Matrix.height(p);
        float sp = Math.max(rad / 60, 1);
        float gap = th * 0.55f;
        float top = cy - th / 2;
        Matrix.draw(canvas, paint, text, cx - Matrix.width(text, p) / 2, top, p, dot, textColor);
        Matrix.draw(canvas, paint, status, cx - Matrix.width(status, sp) / 2, top + th + gap, sp, Theme.DOT, statusColor);
    }

    @Override
    public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info);
        info.setText(text);
    }
}
