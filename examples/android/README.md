# Fern on Android — building a JNI `.so` and packaging an APK

For an app with no Java code, see `native/`: `std/android` runs it in
NativeActivity, and `fern -apk` writes the signed APK with no Android SDK.

Fern compiles to a native, position-independent shared object that an
Android app loads with `System.loadLibrary`. This directory shows the
smallest JNI library. [`stopwatch/`](stopwatch/README.md) is a complete app:
a Fern library, a small Java shell, and a script that builds a signed APK.

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
`static` method) first, then the Java arguments. Each Fern type must have the
width of its Java type, and the compiler refuses one that does not:

| Java | Fern |
|---|---|
| a reference, such as `String`, `Object` or `int[]` | `usize` |
| `boolean` | `u8` |
| `byte`, `char`, `short` | `i8`, `u16`, `i16` |
| `int`, `long` | `i32`, `i64` |
| `float`, `double` | `f32`, `f64` |

The compiler exports each method under its JNI symbol, so `-export` is not
necessary. `-jni-java DIR` writes the Java class of each bound class under
`DIR`, with its `native` declarations and the `System.loadLibrary` call.

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

> **Kotlin-free option:** point `AndroidManifest.xml` at the framework's
> `android.app.NativeActivity` with an `android.app.lib_name` meta-data tag
> and implement `ANativeActivity_onCreate` (a C-ABI export) in Fern — no
> author-written Java. That route trades the Java shim for NDK
> `native_app_glue`-style FFI through `ANativeActivity` (same `__c_call` /
> `__load_ptr` pattern as `std/jni`). The JNI-library route above is the
> smaller, more standard starting point.

## 3. Package the APK

[`stopwatch/build.sh`](stopwatch/build.sh) is a complete recipe with no
Gradle: `aapt` for the resources, `javac` and `d8` for the Java shell,
`zipalign`, then `apksigner`. The Android CI lane runs it and keeps the
APK as a build artifact.

With Gradle, put the `.so` in `src/main/jniLibs/arm64-v8a/` of a standard
app module, and the generated `Native.java` in its sources.

## What's validated where

| piece | validated here | needs your SDK/device |
|---|---|---|
| Fern → arm64/x86-64 `.so` with JNI exports | ✅ (CI) | |
| JNI method ABI (`env`/`thiz`/args, `jint` return) | ✅ (host dlopen) | |
| `JNIEnv` method dispatch via `std/jni` | ✅ (fake-env dlopen) | |
| a desktop JVM calls the JNI methods | ✅ (`TestAndroidStopwatchHostJVM`) | |
| section headers that Android's loader checks | ✅ (`sharedImageCheck`) | |
| `aapt`/`d8`/`zipalign`/`apksigner` packaging | ✅ (`stopwatch/build.sh` in CI) | |
| install + run on a device | — | ✅ |
