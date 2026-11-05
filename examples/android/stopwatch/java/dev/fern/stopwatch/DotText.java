package dev.fern.stopwatch;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;

/** One line of dot-matrix text. Accessibility services read it as text. */
final class DotText extends View {
    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private String text;
    private float pitch;
    private float dot = Theme.DOT;
    private int color;

    DotText(Context context, String text, float pitch, int color) {
        super(context);
        this.text = text;
        this.pitch = pitch;
        this.color = color;
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_YES);
    }

    void setText(String s) {
        if (s.equals(text)) {
            return;
        }
        boolean resize = s.length() != text.length();
        text = s;
        if (resize) {
            requestLayout();
        }
        invalidate();
    }

    void setColor(int argb) {
        color = argb;
        invalidate();
    }

    void setPitch(float px) {
        pitch = px;
        requestLayout();
        invalidate();
    }

    void setThin(boolean thin) {
        dot = thin ? Theme.THIN_DOT : Theme.DOT;
        invalidate();
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
        int w = Math.round(Matrix.width(text, pitch)) + getPaddingLeft() + getPaddingRight();
        int h = Math.round(Matrix.height(pitch)) + getPaddingTop() + getPaddingBottom();
        setMeasuredDimension(resolveSize(w, widthSpec), resolveSize(h, heightSpec));
    }

    @Override
    protected void onDraw(Canvas canvas) {
        float innerW = getWidth() - getPaddingLeft() - getPaddingRight();
        float innerH = getHeight() - getPaddingTop() - getPaddingBottom();
        float x = getPaddingLeft() + (innerW - Matrix.width(text, pitch)) / 2;
        float y = getPaddingTop() + (innerH - Matrix.height(pitch)) / 2;
        Matrix.draw(canvas, paint, text, x, y, pitch, dot, color);
    }

    @Override
    public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info);
        info.setText(text);
    }
}
