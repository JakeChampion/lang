# Fern on Android — building a JNI `.so` and packaging an APK

Fern compiles to a native, position-independent shared object that an
Android app loads with `System.loadLibrary`. This directory shows the
verified-here part (building the `.so`) and documents the SDK-side part
(packaging an installable APK), which needs the Android SDK/NDK and a
device or emulator.

## 1. Build the shared object (verified in CI)

```sh
# arm64 device:
fern -target arm64-android -shared \
     -export Java_dev_fern_demo_Native_answer,Java_dev_fern_demo_Native_jniVersion,Java_dev_fern_demo_Native_greeting,Java_dev_fern_demo_Native_utf8Length,Java_dev_fern_demo_Native_isString,Java_dev_fern_demo_Native_objectHashCode,Java_dev_fern_demo_Native_charCodeAt \
     -o libfern.so examples/android/fern_jni.fern

# x86-64 emulator: -target x86-64-linux (same flags)
```

This emits an `ET_DYN`, W^X, position-independent `.so` with the exported
symbols in its dynamic symbol table — the artifact an APK ships under
`lib/arm64-v8a/libfern.so`. The Fern→`.so` build and the JNI ABI are
covered by the test suite (`internal/e2e/shared_lib_test.go`,
`TestAndroidJNIExampleBuilds`); the `dlopen`+call mechanics are validated
on the host in the same file.

A Fern function is a JNI native method as-is: `(JNIEnv* env, jobject thiz,
args…)` maps to `usize` params (System V / AAPCS64), and an `i32` return is
a `jint`. To call back into the JVM, use `std/jni`: typed wrappers like
`jni.get_version` / `jni.find_class` / `jni.new_string_utf` /
`jni.get_int_field` / `jni.is_instance_of` (built on `jni.call0/1/2/3`),
plus `jni.cstr` to turn a Fern string into the `const char*` the
string/lookup methods expect. To **invoke** a Java method, resolve a
`jmethodID` with `jni.get_method_id`, pack arguments with `jni.jvalues`,
and call `jni.call_int_method_a` / `call_object_method_a` / etc. (the
fixed-arity `Call<Type>MethodA` family).

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

## 3. Package the APK (requires the Android SDK — run on your machine)

Raw build-tools recipe (no Gradle):

```sh
# layout
mkdir -p apk/lib/arm64-v8a
cp libfern.so apk/lib/arm64-v8a/

# compile resources + manifest to a base APK
aapt2 link -o base.apk -I $ANDROID_HOME/platforms/android-34/android.jar \
      --manifest AndroidManifest.xml

# add the native lib, align, sign
(cd apk && zip -r ../base.apk lib)
zipalign -f 4 base.apk app.apk
apksigner sign --ks debug.keystore app.apk

adb install -r app.apk
```

Or with Gradle: a standard library/app module with the `.so` placed in
`src/main/jniLibs/arm64-v8a/` and the `Native` class above.

`AndroidManifest.xml` (minimal, JNI-library + a host `Activity`) is
provided alongside this README.

## What's validated where

| piece | validated here | needs your SDK/device |
|---|---|---|
| Fern → arm64/x86-64 `.so` with JNI exports | ✅ (CI) | |
| JNI method ABI (`env`/`thiz`/args, `jint` return) | ✅ (host dlopen) | |
| `JNIEnv` method dispatch via `std/jni` | ✅ (fake-env dlopen) | |
| `aapt2`/`zipalign`/`apksigner` packaging | recipe only | ✅ |
| install + run on a device | — | ✅ |
