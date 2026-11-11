package e2e

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// An Android kernel can use 16 KiB pages, and maps a load segment only when
// its alignment is a multiple of the page.
func TestAndroidExecutableSegmentAlignment(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { print(\"aligned\"); return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-android", "arm64-android"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(dir, target)
			if o, err := exec.Command(bin, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			f, err := elf.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			loads := 0
			for _, p := range f.Progs {
				if p.Type != elf.PT_LOAD {
					continue
				}
				loads++
				if p.Align%0x4000 != 0 {
					t.Errorf("PT_LOAD at %#x has alignment %#x, want a multiple of 16 KiB", p.Vaddr, p.Align)
				}
				if p.Align != 0 && p.Vaddr%p.Align != p.Off%p.Align {
					t.Errorf("PT_LOAD at %#x: file offset %#x is not congruent to it modulo %#x", p.Vaddr, p.Off, p.Align)
				}
			}
			if loads == 0 {
				t.Fatal("the image has no PT_LOAD segment")
			}
			if target != "x86-64-android" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
				return
			}
			if got, err := exec.Command(out).CombinedOutput(); err != nil || string(got) != "aligned\n" {
				t.Fatalf("run = (%q, %v), want %q", got, err, "aligned\n")
			}
		})
	}
}
