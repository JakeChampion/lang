package dev.fern.stopwatch;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.view.View;
import android.view.accessibility.AccessibilityEvent;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.Switch;

/** An on and off switch: a white pill with a black dot when on, an outline with a grey dot when off. */
final class DotSwitch extends View {
    interface Listener {
        void onChange(boolean checked);
    }

    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final RectF box = new RectF();
    private boolean checked;
    private Listener listener;

    DotSwitch(Context context, boolean checked) {
        super(context);
        this.checked = checked;
        setClickable(true);
        setFocusable(true);
    }

    void setListener(Listener l) {
        listener = l;
    }

    void toggle() {
        checked = !checked;
        invalidate();
        sendAccessibilityEvent(AccessibilityEvent.TYPE_VIEW_CLICKED);
        if (listener != null) {
            listener.onChange(checked);
        }
    }

    @Override
    public boolean performClick() {
        toggle();
        return super.performClick();
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
        setMeasuredDimension(resolveSize(Theme.dp(getContext(), 52), widthSpec),
                resolveSize(Theme.dp(getContext(), 30), heightSpec));
    }

    @Override
    protected void onDraw(Canvas canvas) {
        float t = Math.max(1, Theme.dp(getContext(), 1.5f));
        box.set(t / 2, t / 2, getWidth() - t / 2, getHeight() - t / 2);
        float r = box.height() / 2;
        float cy = getHeight() / 2f;
        if (checked) {
            paint.setStyle(Paint.Style.FILL);
            paint.setColor(Theme.INK);
            canvas.drawRoundRect(box, r, r, paint);
            paint.setColor(Theme.BLACK);
            canvas.drawCircle(box.right - r, cy, r * 0.5f, paint);
        } else {
            paint.setStyle(Paint.Style.STROKE);
            paint.setStrokeWidth(t);
            paint.setColor(Theme.OUTLINE);
            canvas.drawRoundRect(box, r, r, paint);
            paint.setStyle(Paint.Style.FILL);
            paint.setColor(Theme.MUTED);
            canvas.drawCircle(box.left + r, cy, r * 0.42f, paint);
        }
    }

    @Override
    public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info);
        info.setClassName(Switch.class.getName());
        info.setCheckable(true);
        info.setChecked(checked);
    }
}
