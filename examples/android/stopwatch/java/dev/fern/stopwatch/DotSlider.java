package dev.fern.stopwatch;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.os.Bundle;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.accessibility.AccessibilityNodeInfo;
import android.widget.SeekBar;

/** A slider as a row of dots: white up to the value, with a large dot at the value. */
final class DotSlider extends View {
    interface Listener {
        void onChange(int value);
    }

    private static final int DOTS = 24;

    private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final int min;
    private final int max;
    private int value;
    private Listener listener;
    private final int slop;
    private float downX;
    private float downY;
    private boolean dragging;

    DotSlider(Context context, int min, int max, int value) {
        super(context);
        this.min = min;
        this.max = max;
        this.value = value;
        slop = ViewConfiguration.get(context).getScaledTouchSlop();
        setFocusable(true);
        setImportantForAccessibility(IMPORTANT_FOR_ACCESSIBILITY_YES);
    }

    void setListener(Listener l) {
        listener = l;
    }

    private void set(int v) {
        v = Math.max(min, Math.min(max, v));
        if (v == value) {
            return;
        }
        value = v;
        invalidate();
        if (listener != null) {
            listener.onChange(value);
        }
    }

    private float knob() {
        return Theme.dp(getContext(), 7);
    }

    @Override
    protected void onMeasure(int widthSpec, int heightSpec) {
        setMeasuredDimension(resolveSize(Theme.dp(getContext(), 240), widthSpec),
                resolveSize(Theme.dp(getContext(), 32), heightSpec));
    }

    @Override
    protected void onDraw(Canvas canvas) {
        float k = knob();
        float step = (getWidth() - 2 * k) / (DOTS - 1);
        float cy = getHeight() / 2f;
        int at = Math.round((float) (value - min) / Math.max(1, max - min) * (DOTS - 1));
        float small = Theme.dp(getContext(), 2.4f);
        paint.setStyle(Paint.Style.FILL);
        for (int i = 0; i < DOTS; i++) {
            paint.setColor(i <= at ? Theme.INK : Theme.OUTLINE);
            canvas.drawCircle(k + i * step, cy, i == at ? k : small, paint);
        }
    }

    private void moveTo(float x) {
        float k = knob();
        float frac = Math.max(0, Math.min(1, (x - k) / Math.max(1, getWidth() - 2 * k)));
        set(min + Math.round(frac * (max - min)));
    }

    /**
     * A drag starts after a move to the side, so that a vertical swipe still
     * scrolls the screen. A tap sets the value at once.
     */
    @Override
    public boolean onTouchEvent(MotionEvent event) {
        if (!isEnabled()) {
            return false;
        }
        switch (event.getActionMasked()) {
            case MotionEvent.ACTION_DOWN:
                downX = event.getX();
                downY = event.getY();
                dragging = false;
                return true;
            case MotionEvent.ACTION_MOVE:
                if (!dragging) {
                    float dx = Math.abs(event.getX() - downX);
                    if (dx > slop && dx > Math.abs(event.getY() - downY)) {
                        dragging = true;
                        setPressed(true);
                        getParent().requestDisallowInterceptTouchEvent(true);
                    }
                }
                if (dragging) {
                    moveTo(event.getX());
                }
                return true;
            case MotionEvent.ACTION_UP:
                if (!dragging) {
                    moveTo(event.getX());
                }
                // fall through
            case MotionEvent.ACTION_CANCEL:
                dragging = false;
                setPressed(false);
                return true;
            default:
                return true;
        }
    }

    @Override
    public void onInitializeAccessibilityNodeInfo(AccessibilityNodeInfo info) {
        super.onInitializeAccessibilityNodeInfo(info);
        info.setClassName(SeekBar.class.getName());
        info.setRangeInfo(AccessibilityNodeInfo.RangeInfo.obtain(
                AccessibilityNodeInfo.RangeInfo.RANGE_TYPE_INT, min, max, value));
        if (value > min) {
            info.addAction(AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD);
        }
        if (value < max) {
            info.addAction(AccessibilityNodeInfo.ACTION_SCROLL_FORWARD);
        }
    }

    @Override
    public boolean performAccessibilityAction(int action, Bundle arguments) {
        if (action == AccessibilityNodeInfo.ACTION_SCROLL_FORWARD) {
            set(value + 1);
            return true;
        }
        if (action == AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD) {
            set(value - 1);
            return true;
        }
        return super.performAccessibilityAction(action, arguments);
    }
}
