package e2e

import (
	"bytes"
	"debug/elf"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAndroidJNIExampleBuilds compiles examples/android/fern_jni.fern with
// `fern -target arm64-android -shared -export <jni symbols>` and checks the
// result is a position-independent (ET_DYN) AArch64 shared object whose
// JNI export symbols are present in .dynstr — the artifact an APK ships
// under lib/arm64-v8a/. (The dlopen+call mechanics are covered on the host
// by shared_lib_test.go; this guards the documented example + the
// JNI-mangled export names against bit-rot.)
func TestAndroidJNIExampleBuilds(t *testing.T) {
	bin := buildFernCLI(t)
	src, err := filepath.Abs("../../examples/android/fern_jni.fern")
	if err != nil {
		t.Fatal(err)
	}
	syms := []string{
		"Java_dev_fern_demo_Native_answer",
		"Java_dev_fern_demo_Native_jniVersion",
		"Java_dev_fern_demo_Native_greeting",
		"Java_dev_fern_demo_Native_utf8Length",
		"Java_dev_fern_demo_Native_isString",
		"Java_dev_fern_demo_Native_objectHashCode",
		"Java_dev_fern_demo_Native_charCodeAt",
	}
	exports := strings.Join(syms, ",")
	so := filepath.Join(t.TempDir(), "libfern.so")
	if o, err := exec.Command(bin, "-target", "arm64-android", "-shared",
		"-export", exports, "-o", so, src).CombinedOutput(); err != nil {
		t.Fatalf("build example .so: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(so)
	if err != nil {
		t.Fatal(err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	if f.Type != elf.ET_DYN || f.Machine != elf.EM_AARCH64 {
		t.Errorf("type/machine = %v/%v, want ET_DYN/AArch64", f.Type, f.Machine)
	}
	for _, sym := range syms {
		if !bytes.Contains(raw, append([]byte(sym), 0)) {
			t.Errorf(".dynstr missing export %q", sym)
		}
	}
}

// TestAndroidManifestValid keeps the example AndroidManifest.xml well-formed
// (aapt2 needs valid XML to compile it to binary form).
func TestAndroidManifestValid(t *testing.T) {
	raw, err := os.ReadFile("../../examples/android/AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := xml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("AndroidManifest.xml is not well-formed XML: %v", err)
	}
}
