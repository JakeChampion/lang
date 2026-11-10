# The keyboard of the system

This module shows the keyboard of the system for a text field, for a NativeActivity app of `std/android`.

## Model

A keyboard of the system types into a view that edits text. A NativeActivity has no such view. A keyboard that sends key events then types through `Key` and `Char` events. But most input methods send text, not keys: glide input, voice input, emoji, and most scripts other than Latin.

`show` puts a hidden `EditText` in the window, gives it the focus, and shows the keyboard. The keyboard then edits the text of that field. After each change, the loop thread gets the whole text of the field as an `android.Text` event. The action key of the keyboard, such as Go or Done, comes as an `android.TextAction` event with its action code.

Keys of a hardware keyboard still come as `Key` and `Char` events, because the native input queue takes them. When the app changes its text for such a key, `set_text` gives the field the same text. A change that `set_text` makes does not come back as a `Text` event.

The module declares the class `dev.fern.android.TextField`, which watches the field. So the APK of an app that imports this module holds that class. A JNI method needs a native target, so a program for the host cannot import this module.

## Contracts

Call `show`, `set_text` and `hide` on the loop thread, with the context that `android.start` gave the loop.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<field>>

<<watcher>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module shows the keyboard of the system for a text field.
import "std/android";
```

### `field`

The input type and the action code are the values of `InputType` and `EditorInfo`, such as `android.INPUT_TEXT` and `android.ACTION_GO`.

```fern
<<field>>=
// Shows the keyboard for a field that holds `text`, with the input type
// `input` and the action key `action`.
pub function show(ctx: usize, text: string, input: i32, action: i32): void {
  android.show_field(ctx, text, input, action);
}

// Gives the field the text `text`, for a change that the app made.
pub function set_text(ctx: usize, text: string): void {
  android.set_field(ctx, text);
}

// Hides the keyboard and takes the focus from the field.
pub function hide(ctx: usize): void {
  android.hide_field(ctx);
}
```

### `watcher`

The field calls these methods on the UI thread.

```fern
<<watcher>>=
@jni("dev.fern.android.TextField implements android.text.TextWatcher, android.widget.TextView$OnEditorActionListener", "void beforeTextChanged(java.lang.CharSequence, int, int, int)")
pub function before_change(env: usize, thiz: usize, s: usize, start: i32, count: i32, after: i32): void {}

@jni("dev.fern.android.TextField", "void onTextChanged(java.lang.CharSequence, int, int, int)")
pub function on_change(env: usize, thiz: usize, s: usize, start: i32, before: i32, count: i32): void {}

@jni("dev.fern.android.TextField", "void afterTextChanged(android.text.Editable)")
pub function after_change(env: usize, thiz: usize, editable: usize): void {
  android.text_changed(env, thiz, editable);
}

@jni("dev.fern.android.TextField", "boolean onEditorAction(android.widget.TextView, int, android.view.KeyEvent)")
pub function editor_action(env: usize, thiz: usize, view: usize, action: i32, event: usize): u8 {
  if (android.text_action(env, thiz, action)) {
    return 1u8;
  }
  return 0u8;
}
```
