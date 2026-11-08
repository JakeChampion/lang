# Fern on Android — building a JNI `.so` and packaging an APK

An app with no Java code runs in NativeActivity through `std/android`, and
`fern -apk` writes the signed APK with no Android SDK. `native/` is the
smallest such app. [`stopwatch/`](stopwatch/README.md),
[`walkie/`](walkie/README.md), [`plex/`](plex/README.md), a Plex client,
and [`paper/`](paper/README.md), a home screen, are complete apps. They share the dot font of
`matrix.fern` and the look of `look.fern`.

Fern also compiles to a native, position-independent shared object that an
Android app with Java code loads with `System.loadLibrary`. This directory
shows the smallest JNI library.

## 1. Build the shared object (verified in CI)

```sh
# arm64 device:
fern -target arm64-android -shared -jni-java java \
     -o libfern.so examples/android/fern_jni.fern

# x86-64 emulator: -target x86-64-android (same flags)
```

This emits an `ET_DYN`, W^X, position-independent `.so`. Its dynamic symbol
table has the exported functions, and it has the section headers that
Android's loader checks. An APK ships it as `lib/arm64-v8a/libfern.so`.
`internal/testing/e2e/shared_lib_test.go` and `TestAndroidJNIExampleBuilds`
test the build and the JNI ABI. The same file loads a library on the host
with `dlopen` and calls it.

Android discards the stderr of an app. On the Android targets, each line
that Fern writes to stderr also goes to logcat at priority W. A `std/log`
record is such a line. An abort report goes to logcat at priority F. The tag is
`fern`, so `adb logcat -s fern` shows them.

`@jni` binds a Fern function to a Java native method. Its arguments are the
Java class and the Java declaration:

```fern
@jni("dev.fern.demo.Native", "int charCodeAt(String, int)")
pub function char_code_at(env: usize, thiz: usize, s: usize, i: i32): i32 {
```

The function takes the `JNIEnv*` and the `jobject` (or the `jclass` of a
`static` method) first, then the Java arguments. Each Java type has the Fern
type in this table, and the compiler refuses a different one. The wrapper
extends a `byte` or a `short` with its sign and a `char` with zeros, so the
Fern function gets the Java value:

| Java | Fern |
|---|---|
| a reference, such as `String`, `Object` or `int[]` | `usize` |
| `boolean` | `u8` |
| `byte`, `char`, `short`, `int` | `i32` |
| `long` | `i64` |
| `float`, `double` | `f32`, `f64` |

The compiler exports each method under its JNI symbol, so `-export` is not
necessary. `-jni-java DIR` writes the Java class of each bound class under
`DIR`, with its `native` declarations and the `System.loadLibrary` call.

The class argument can be a Java class head, so that a class implements a
listener interface or extends a framework class. For example,
`@jni("dev.fern.demo.Tick implements java.lang.Runnable", "void run()")`.
Name the head on one method of the class. The other methods of the class
can give the name alone. Give an object type of a method with its full name,
such as `android.content.Intent`. `String` and `Object` are the two
exceptions.

`-jni-dex FILE` writes the bound classes to a DEX file, with no Java source.
Each class gets a public constructor and a static initializer that calls
`System.loadLibrary`. `fern -apk` uses this flag. An app with `@jni` classes
gets `classes.dex`, and a class that extends `android.app.NativeActivity` is
its activity. `tests/android/dex` is an example that the emulator lane runs.

To call back into the JVM, use `std/jni`. Its typed wrappers, such as
`jni.get_version`, `jni.find_class` and `jni.get_int_field`, use
`jni.call0/1/2/3`. The lookups take Fern strings, for example
`jni.find_class(env, "java/lang/String")`. To return a Java string, use
`jni.new_string(env, s)`. These functions free their temporary C copies.
For a method that keeps a `const char*`, make it with `jni.cstr`, and free it
with `jni.free_buffer`.

To call a C function of an NDK library, such as `liblog.so` or
`libandroid.so`, declare it with `@import("liblog.so", "__android_log_write")`
on a function with no body. The library loads with the `.so`.

To give C a pointer to a Fern function, as a C callback, declare
`@callback("on_input") function on_input_entry(): usize;` with no body. The
declaration returns the address of the C entry of `on_input`. The entry takes
and returns the C types of the parameters and the result of `on_input`, as an
`-export` entry does. The entry is not a dynamic symbol.

To **invoke** a Java method, resolve a `jmethodID` with `jni.get_method_id`.
Pack the arguments with `jni.jvalues`. Then call `jni.call_int_method_a`,
`call_object_method_a` or another method of the fixed-arity
`Call<Type>MethodA` family. Free the arguments with `jni.free_buffer`.

## 2. The Java side

`-jni-java` writes `java/dev/fern/demo/Native.java` for this example: a
class with one `native` method for each `@jni` function. Compile it with the
rest of the app, in Java or in Kotlin.

An app with no Java code at all needs no class of its own: `std/android`
runs it in NativeActivity, as in `native/`, `stopwatch/` and `walkie/`.

## 3. Package the APK

`fern -apk` packages an app with no Java code. By default it stores each
library, aligned for 16 KB pages, and the manifest sets
`android:extractNativeLibs` to false. Android then maps the library from
the APK, and the installed app is the APK alone. `-apk-compress` deflates
the libraries, and Android extracts them at install. The file is smaller,
but the installed app is the APK and the libraries:

| stopwatch | APK file | installed |
|---|---|---|
| stored (default) | 589 KB | 589 KB |
| `-apk-compress` | 131 KB | 131 KB and 280 KB for the library of the ABI |

The Android emulator check records exact measurements for both library forms
in `native/installed-size.json` and `native-compressed/installed-size.json`
inside the `fern-stopwatch-emulator` CI artifact. Each record includes the
download file size, the APK and extracted library sizes on the device, and
the allocated storage of the installation directory before the app starts.
The measurements exclude app data. They distinguish file sizes from allocated
storage. Directory and block allocation can make these totals differ.

Use the default for an app from a store, which compresses the download
itself. Use `-apk-compress` for an APK that people download as a file.

For an app with a Java
shell, use the Android SDK: `aapt` for the resources, `javac` and `d8` for
the Java code, `zipalign`, then `apksigner`. With Gradle, put the `.so` in
`src/main/jniLibs/arm64-v8a/` of a standard app module, and the generated
`Native.java` in its sources.

## What's validated where

| piece | validated here | needs your SDK/device |
|---|---|---|
| Fern → arm64/x86-64 `.so` with JNI exports | ✅ (CI) | |
| JNI method ABI (`env`/`thiz`/args, `jint` return) | ✅ (host dlopen) | |
| `JNIEnv` method dispatch via `std/jni` | ✅ (fake-env dlopen) | |
| section headers that Android's loader checks | ✅ (`sharedImageCheck`) | |
| `fern -apk` packaging | ✅ (`TestApk*`, and the emulator lane in CI) | |
| install + run on a device | — | ✅ |
