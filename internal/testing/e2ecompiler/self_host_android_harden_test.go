//go:build linux

package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// -harden on the Android targets. The arm64-android executable and shared
// object carry the BTI property note, both Android executables run under
// -harden, and -profile hardened builds for both.
func TestSelfHostHardenAndroid(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(selfHostHardenSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(t *testing.T, target, out string, flags ...string) {
		t.Helper()
		args := append([]string{"-target", target}, flags...)
		args = append(args, "-o", out, src, stdlib)
		cmd := exec.Command(cli, args...)
		cmd.Env = e2eharness.ChildEnv()
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %v: %v\n%s", flags, err, msg)
		}
	}
	run := func(t *testing.T, target, bin string) string {
		t.Helper()
		var cmd *exec.Cmd
		if target == "x86-64-android" {
			cmd = e2eharness.RunX86_64Bin(e2eharness.X86_64Runner(t), bin)
		} else if runner := e2eharness.Arm64Runner(t); runner != "" {
			cmd = exec.Command(runner, bin)
		} else {
			cmd = exec.Command(bin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", filepath.Base(bin), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pad := binary.LittleEndian.AppendUint32(nil, 0xd503245f)
	for _, target := range []string{"arm64-android", "x86-64-android"} {
		t.Run(target, func(t *testing.T) {
			arm64 := target == "arm64-android"
			plain := filepath.Join(dir, target+"-plain")
			hard := filepath.Join(dir, target+"-hard")
			compile(t, target, plain)
			compile(t, target, hard, "-harden")
			for _, bin := range []string{plain, hard} {
				f, err := elf.Open(bin)
				if err != nil {
					t.Fatal(err)
				}
				if f.Type != elf.ET_DYN {
					t.Errorf("%s has type %v, want ET_DYN", filepath.Base(bin), f.Type)
				}
				f.Close()
				if out := run(t, target, bin); out != hardenWant {
					t.Errorf("%s printed %q, want %q", filepath.Base(bin), out, hardenWant)
				}
			}
			if btiNote(t, plain) {
				t.Error("an executable built without -harden asks for BTI")
			}
			if btiNote(t, hard) != arm64 {
				t.Errorf("the -harden executable has a BTI property note: %t, want %t", !arm64, arm64)
			}
			if arm64 {
				img, err := os.ReadFile(hard)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Count(img, pad) < 2 {
					t.Errorf("the -harden executable has %d landing pads, want one per address-taken function", bytes.Count(img, pad))
				}
			}

			so := filepath.Join(dir, target+".so")
			plainSo := filepath.Join(dir, target+"-plain.so")
			compile(t, target, so, "-shared", "-harden")
			compile(t, target, plainSo, "-shared")
			if btiNote(t, plainSo) {
				t.Error("a shared object built without -harden asks for BTI")
			}
			if btiNote(t, so) != arm64 {
				t.Errorf("the -harden shared object has a BTI property note: %t, want %t", !arm64, arm64)
			}

			profiled := filepath.Join(dir, target+"-hardened")
			compile(t, target, profiled, "-profile", "hardened")
			if btiNote(t, profiled) != arm64 {
				t.Errorf("the hardened profile image has a BTI property note: %t, want %t", !arm64, arm64)
			}
			if out := run(t, target, profiled); out != hardenWant {
				t.Errorf("the hardened profile image printed %q, want %q", out, hardenWant)
			}
		})
	}
}
