package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The SSA backends link their own output and return before the flag handling
// further down in run(), so a flag served only down there never reaches them.
// Both of these used to pass silently and produce something other than what
// was asked for:
//
//   - -shared linked an ordinary executable (ELF type EXEC) where the default
//     emitter produces a shared object (ELF type DYN);
//   - -g emitted .debug_info with no .debug_line, so a debugger had symbol
//     names and no way to map an address back to a source line.
//
// -cover was refused already, but by the lowering, which knows targets and not
// backends: it told a build that had just asked for x86-64-linux to build for
// x86-64-linux. The refusal belongs where the backend is known.
//
// A build that cannot do what the flags ask has to say so. These are gaps to
// close, not decisions, and the message points at the build that serves them.
func TestSSABackendRefusesFlagsItCannotServe(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 { return 7; }\n")

	for _, tc := range []struct {
		flag  string
		wants []string
	}{
		{"-shared", []string{"-shared", "shared-object", "executable"}},
		{"-g", []string{"-g", "line table", ".debug_line"}},
		{"-cover", []string{"-cover", "instrumentation", "build without -backend ssa"}},
	} {
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(tc.flag+"_"+target, func(t *testing.T) {
				out := filepath.Join(t.TempDir(), "prog")
				got, err := exec.Command(bin, "-target", target, "-backend", "ssa", tc.flag, "-o", out, entry).CombinedOutput()
				if err == nil {
					t.Fatalf("-backend ssa %s should be refused, not built silently:\n%s", tc.flag, got)
				}
				for _, want := range tc.wants {
					if !strings.Contains(string(got), want) {
						t.Errorf("refusal missing %q:\n%s", want, got)
					}
				}
			})
		}
	}
}

// The guard is about the flags the backend cannot serve, so an ordinary build
// on the same targets still goes through it.
func TestSSABackendStillBuildsWithoutThoseFlags(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 { return 7; }\n")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "prog")
			if got, err := exec.Command(bin, "-target", target, "-backend", "ssa", "-o", out, entry).CombinedOutput(); err != nil {
				t.Fatalf("plain -backend ssa build failed: %v\n%s", err, got)
			}
		})
	}
}
