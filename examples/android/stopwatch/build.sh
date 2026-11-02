#!/usr/bin/env bash
# Builds build/android-stopwatch/fern-stopwatch.apk in the repository root.
# The Fern logic is libfern.so, for arm64 devices and x86-64 emulators. A
# small Java shell shows the Android views.
#
# Tools: aapt, d8 or dx, zipalign and apksigner from the newest build-tools
# of the Android SDK, and the newest platforms/android-*/android.jar. On
# Ubuntu, the aapt, dalvik-exchange, zipalign, apksigner and
# android-sdk-platform-23 packages install an SDK at /usr/lib/android-sdk.
#
# Environment:
#   FERN          the Fern compiler (default: bin/fern of this repository)
#   ANDROID_HOME  the Android SDK (default: /usr/lib/android-sdk)
#   KEYSTORE      the signing keystore; a debug keystore is made if unset
#   KEYSTORE_PASS its password (default: android)
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
out="$repo/build/android-stopwatch"
fern=${FERN:-$repo/bin/fern}

sdk=${ANDROID_HOME:-/usr/lib/android-sdk}
tools=$(ls -d "$sdk"/build-tools/* 2>/dev/null | sort -V | tail -n 1)
[ -n "$tools" ] && PATH="$tools:$PATH"
android_jar=$(ls "$sdk"/platforms/android-*/android.jar 2>/dev/null | sort -V | tail -n 1)
for tool in aapt zipalign apksigner javac keytool; do
  command -v "$tool" >/dev/null || { echo "build.sh: no $tool in $sdk/build-tools or on PATH" >&2; exit 1; }
done
command -v d8 >/dev/null || command -v dx >/dev/null || { echo "build.sh: no d8 or dx" >&2; exit 1; }
[ -n "$android_jar" ] || { echo "build.sh: no $sdk/platforms/android-*/android.jar" >&2; exit 1; }
[ -x "$fern" ] || { echo "build.sh: no Fern compiler at $fern (run make build)" >&2; exit 1; }

rm -rf "$out"
mkdir -p "$out/gen" "$out/classes" "$out/apk/lib/arm64-v8a" "$out/apk/lib/x86_64"

exports=$(sed -n 's/^pub function \(Java_[A-Za-z0-9_]*\).*/\1/p' "$here/native.fern" | paste -sd, -)
"$fern" -target arm64-android -shared -export "$exports" \
  -o "$out/apk/lib/arm64-v8a/libfern.so" "$here/native.fern"
"$fern" -target x86-64-android -shared -export "$exports" \
  -o "$out/apk/lib/x86_64/libfern.so" "$here/native.fern"

aapt package -f -m -J "$out/gen" -M "$here/AndroidManifest.xml" -S "$here/res" \
  -I "$android_jar" -F "$out/unsigned.apk"

find "$here/java" "$out/gen" -name '*.java' > "$out/sources.txt"
javac -nowarn --release 8 -Xlint:-options -classpath "$android_jar" -d "$out/classes" @"$out/sources.txt"

if command -v d8 >/dev/null; then
  d8 --release --min-api 23 --lib "$android_jar" --output "$out/apk" $(find "$out/classes" -name '*.class')
else
  dx --dex --min-sdk-version=23 --output="$out/apk/classes.dex" "$out/classes"
fi

(cd "$out/apk" && aapt add "$out/unsigned.apk" classes.dex lib/arm64-v8a/libfern.so lib/x86_64/libfern.so >/dev/null)
zipalign -f -p 4 "$out/unsigned.apk" "$out/aligned.apk"

keystore=${KEYSTORE:-$out/debug.keystore}
pass=${KEYSTORE_PASS:-android}
if [ -z "${KEYSTORE:-}" ]; then
  keytool -genkeypair -keystore "$keystore" -storepass "$pass" -keypass "$pass" \
    -alias debug -keyalg RSA -keysize 2048 -validity 10000 \
    -dname "CN=Fern Stopwatch debug" >/dev/null 2>&1
fi
apksigner sign --ks "$keystore" --ks-pass "pass:$pass" --out "$out/fern-stopwatch.apk" "$out/aligned.apk"
apksigner verify "$out/fern-stopwatch.apk"
echo "$out/fern-stopwatch.apk"
