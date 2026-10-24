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

func TestAndroidJNIExampleBuilds(t *testing.T) {
	bin := buildFernCLI(t)
	src, err := filepath.Abs("../../../examples/android/fern_jni.fern")
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

func TestAndroidManifestValid(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/android/AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := xml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("AndroidManifest.xml is not well-formed XML: %v", err)
	}
}
