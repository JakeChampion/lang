# A to-do list with `std/android/ui`

This app keeps a list of things to do. A person types an item into the
text field and adds it with the Add button or with the action key of the
keyboard. Each item has a switch for done and a button that removes it.
The list scrolls.

- `todo.fern` is the program, with no Android code: the `Model`, the
  `Msg`, the `view` and the `update`. `Msg` is the type of its messages.
  `ui.list` makes only the rows that show.
- `app.fern` is the NativeActivity. `std/android/ui/app` runs the program
  on the phone, with the keyboard of the system.
- `todo_test.fern` runs the program on the host with `std/android/ui/sim`:
  `sim.type_in` types into the field, and `sim.enter` presses the action
  key.

Build and run it on a phone or an emulator:

```sh
fern -apk todo.apk -apk-run examples/android/todo/app.fern
```

The [nightly release](https://github.com/JakeChampion/lang/releases/tag/nightly) has a signed `fern-todo.apk` from `main`.

## Tests

`internal/testing/e2e/android_todo_test.go` runs two checks:

- `TestAndroidTodoProgram` runs `todo_test.fern`. With `TODO_UI_PNG` set
  to a directory, the test writes the screen there as a PNG file.
- `TestAndroidTodoApk` builds the APK and checks its entries.

The Android CI lane runs `emulator_check.py`. It types two items with key
events, presses the back key, and requires the two items in logcat.
