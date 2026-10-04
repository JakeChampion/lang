package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestXattrBytes(t *testing.T) {
	fern := buildLangBinForInterp(t)
	for _, target := range []string{"interp", "arm64-darwin", "x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			actual := target
			var runner []string
			switch actual {
			case "arm64-darwin":
				if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
					t.Skip("requires Apple Silicon")
				}
			case "x86-64-linux":
				if runtime.GOOS != "linux" {
					t.Skip("Linux attributes require a Linux host")
				}
				_, runner = x86_64Tooling(t)
			case "arm64-linux":
				if runtime.GOOS != "linux" {
					t.Skip("Linux attributes require a Linux host")
				}
				_, qemu := arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			}
			fixture := e2eharness.MakeXattrBytesFixture(t)
			src := filepath.Join(t.TempDir(), "xattr.fern")
			if err := os.WriteFile(src, []byte(fixture.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			var argv []string
			if actual == "interp" {
				argv = []string{fern, "-interp", src}
			} else {
				bin := filepath.Join(t.TempDir(), "xattr")
				args := []string{"-target", actual, "-o", bin, src}
				if out, err := exec.Command(fern, args...).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, out)
				}
				argv = append(append([]string{}, runner...), bin)
			}
			if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			fixture.CheckWrites(t)
		})
	}
}
