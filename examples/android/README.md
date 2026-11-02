# Fern on Android — building a JNI `.so` and packaging an APK

Fern compiles to a native, position-independent shared object that an
Android app loads with `System.loadLibrary`. This directory shows the
smallest JNI library. [`stopwatch/`](stopwatch/README.md) is a complete app:
a Fern library, a small Java shell, and a script that builds a signed APK.

## 1. Build the shared object (verified in CI)

```sh
# arm64 device:
fern -target arm64-android -shared \
     -export Java_dev_fern_demo_Native_answer,Java_dev_fern_demo_Native_jniVersion,Java_dev_fern_demo_Native_greeting,Java_dev_fern_demo_Native_utf8Length,Java_dev_fern_demo_Native_isString,Java_dev_fern_demo_Native_objectHashCode,Java_dev_fern_demo_Native_charCodeAt \
     -o libfern.so examples/android/fern_jni.fern

# x86-64 emulator: -target x86-64-linux (same flags)
```

This emits an `ET_DYN`, W^X, position-independent `.so`. Its dynamic symbol
table has the exported functions, and it has the section headers that
Android's loader checks. An APK ships it as `lib/arm64-v8a/libfern.so`.
`internal/testing/e2e/shared_lib_test.go` and `TestAndroidJNIExampleBuilds`
test the build and the JNI ABI. The same file loads a library on the host
with `dlopen` and calls it.

A Fern function is a JNI native method as-is, `(JNIEnv* env, jobject thiz,
args…)`. Give each argument and the result a Fern type of the same width:

| JNI | Fern |
|---|---|
| `JNIEnv*`, `jobject`, `jclass`, `jstring` | `usize` |
| `jint` | `i32` |
| `jlong` | `i64` |
| `jboolean` | `u8` |

To call back into the JVM, use `std/jni`. Its typed wrappers, such as
`jni.get_version`, `jni.find_class` and `jni.get_int_field`, use
`jni.call0/1/2/3`. `jni.cstr` makes the `const char*` that the lookup
methods expect. Free it with `jni.free_buffer` after the call. To return a
Java string, use `jni.new_string(env, s)`, which frees its temporary copy.

To **invoke** a Java method, resolve a `jmethodID` with `jni.get_method_id`.
Pack the arguments with `jni.jvalues`. Then call `jni.call_int_method_a`,
`call_object_method_a` or another method of the fixed-arity
`Call<Type>MethodA` family. Free the arguments with `jni.free_buffer`.

## 2. The Java/Kotlin side (one tiny class)

```kotlin
package dev.fern.demo
class Native {
    external fun answer(): Int
    external fun jniVersion(): Int
    external fun greeting(): String
    external fun utf8Length(s: String): Int
    external fun isString(obj: Any): Boolean
    external fun objectHashCode(obj: Any): Int
    external fun charCodeAt(s: String, i: Int): Int
    companion object { init { System.loadLibrary("fern") } }
}
```

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
`zipalign`, then `apksigner`. The Examples CI lane runs it and keeps the
APK as a build artifact.

With Gradle, put the `.so` in `src/main/jniLibs/arm64-v8a/` of a standard
app module, beside the `Native` class above.

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
