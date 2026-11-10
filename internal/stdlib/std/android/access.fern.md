# Accessibility services

This module gives an accessibility service, such as TalkBack, a tree of items to read. It is for a NativeActivity app of `std/android`.

## Model

A NativeActivity draws its own pixels, so an accessibility service finds no view to read in its window. The app calls `publish` with the items that it shows. Each `android.AccessItem` has an id, a rectangle in the pixels of the window, a text and its actions. The service reads the text.

The service then reads the items and moves its focus from item to item. While TalkBack explores by touch, the item under the finger gets the focus. A double tap on an item comes to the app as an `android.Access` event. The event has the id of the item and `android.ACCESS_CLICK`. A double tap and hold comes with `android.ACCESS_LONG_CLICK`.

An item with `ACCESS_CLICK` has the role of a button. An item with no action has the role of text.

The module declares the classes `dev.fern.android.AccessDelegate` and `dev.fern.android.AccessTree`, so the APK of an app that imports this module holds them. A JNI method needs a native target, so a program for the host cannot import this module.

## Contracts

Call `publish` on the loop thread after each change of what the app shows. Give it the context that `android.start` gave the loop. A call with the same items as the last call sends nothing. So a call for each frame costs only the bytes of the items. The id of an item is 0 or more, and each id appears once in a tree.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<tree>>

<<delegate>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module gives an accessibility service a tree of items to read.
import "std/android";
```

### `tree`

```fern
<<tree>>=
// Gives the service the items `items`, when they differ from the last items.
pub function publish(ctx: usize, items: android.AccessItem[]): void {
  android.publish_tree(ctx, items);
}
```

### `delegate`

The view and the service call these methods on the UI thread.

```fern
<<delegate>>=
@jni("dev.fern.android.AccessDelegate extends android.view.View$AccessibilityDelegate", "android.view.accessibility.AccessibilityNodeProvider getAccessibilityNodeProvider(android.view.View)")
pub function provider(env: usize, thiz: usize, host: usize): usize {
  return android.access_provider(env, thiz);
}

@jni("dev.fern.android.AccessTree extends android.view.accessibility.AccessibilityNodeProvider", "android.view.accessibility.AccessibilityNodeInfo createAccessibilityNodeInfo(int)")
pub function node(env: usize, thiz: usize, id: i32): usize {
  return android.access_node(env, thiz, id);
}

@jni("dev.fern.android.AccessTree", "android.view.accessibility.AccessibilityNodeInfo findFocus(int)")
pub function focus(env: usize, thiz: usize, kind: i32): usize {
  return android.access_focus(env, thiz, kind);
}

@jni("dev.fern.android.AccessTree", "boolean performAction(int, int, android.os.Bundle)")
pub function action(env: usize, thiz: usize, id: i32, action: i32, args: usize): u8 {
  if (android.access_action(env, thiz, id, action, args)) {
    return 1u8;
  }
  return 0u8;
}
```
