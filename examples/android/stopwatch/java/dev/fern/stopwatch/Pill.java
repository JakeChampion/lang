package dev.fern.stopwatch;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.view.View;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.Button;

/** A rounded button with its label in dots: filled, or only its outline. */
final class Pill extends View {
    static final int FILLED = 0;
    static final int OUTLINED = 1;

    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final RectF box = new RectF();
    private final String label;
    private final int style;
    private final int color;

    Pill(Context context, String label, int style, int color) {
        super(context);
        this.label = label;
        this.style = style;
        this.color = color;
        setClickable(true);
        setFocusable(true);
    }

    private float pitch() {
        return Theme.dp(getContext(), 44) * 0.4f / Matrix.ROWS;
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
        int h = Theme.dp(getContext(), 44);
        int w = Math.round(Matrix.width(label, pitch()) + h * 1.1f);
        setMeasuredDimension(resolveSize(w, widthSpec), resolveSize(h, heightSpec));
    }

    @Override
    protected void drawableStateChanged() {
        super.drawableStateChanged();
        invalidate();
    }

    @Override
    protected void onDraw(Canvas canvas) {
        float t = Math.max(1, Theme.dp(getContext(), 1.5f));
        box.set(t / 2, t / 2, getWidth() - t / 2, getHeight() - t / 2);
        float r = box.height() / 2;
        int text = color;
        if (style == FILLED) {
            paint.setStyle(Paint.Style.FILL);
            paint.setColor(isPressed() ? Theme.MUTED : color);
            canvas.drawRoundRect(box, r, r, paint);
            text = Theme.BLACK;
        } else {
            if (isPressed()) {
                paint.setStyle(Paint.Style.FILL);
                paint.setColor(Theme.SURFACE);
                canvas.drawRoundRect(box, r, r, paint);
            }
            paint.setStyle(Paint.Style.STROKE);
            paint.setStrokeWidth(t);
            paint.setColor(color);
            canvas.drawRoundRect(box, r, r, paint);
        }
        float p = pitch();
        Matrix.draw(canvas, paint, label, (getWidth() - Matrix.width(label, p)) / 2,
                (getHeight() - Matrix.height(p)) / 2, p, Theme.DOT, text);
    }

    @Override
    public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info);
        info.setClassName(Button.class.getName());
        info.setText(label);
    }
}
