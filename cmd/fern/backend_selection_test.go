package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

const defaultBackendProg = `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function main(): i32 { return fib(9); }`

// buildWith builds prog for target under backend and returns the image.
func buildWith(t *testing.T, dir, target, backend, name string) []byte {
	t.Helper()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte(defaultBackendProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name)
	if code, err := run(src, out, target, backend, "", "", false, true, "", false, false, false, nil, false, "", false, nil); err != nil || code != 0 {
		t.Fatalf("build %s with -backend %q: code=%d err=%v", target, backend, code, err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The default emitter, per target. arm64-linux is the SSA backend: it emits
// less code, and the corpus differential runs both on every change. Every
// other target keeps the stack-machine emitter — x86-64-linux because the SSA
// one is still larger there (#4112).
//
// Byte-identity is the assertion in both directions, so this fails if a target
// silently changes which emitter it gets.
func TestDefaultBackendPerTarget(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"arm64-linux", "ssa"},
		{"x86-64-linux", "flat"},
		{"wasm32-wasi", "flat"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			dir := t.TempDir()
			dflt := buildWith(t, dir, tc.target, "", "dflt")
			want := buildWith(t, dir, tc.target, tc.want, tc.want)
			if !bytes.Equal(dflt, want) {
				t.Errorf("the default for %s is not -backend %s: %d bytes against %d", tc.target, tc.want, len(dflt), len(want))
			}
			// And it is genuinely the other one, not both names reaching the
			// same emitter — otherwise the check above proves nothing.
			if tc.target == "arm64-linux" {
				if flat := buildWith(t, dir, tc.target, "flat", "flat"); bytes.Equal(dflt, flat) {
					t.Errorf("-backend ssa and -backend flat produced identical images on %s, so this test cannot tell them apart", tc.target)
				}
			}
		})
	}
}

// A flag the SSA backend cannot serve keeps the emitter that can, rather than
// failing a build that never named a backend. Asking for `-backend ssa`
// alongside one of these is still an error — that is ssaUnservedFlag, and
// TestSSABackendRefusesFlagsItCannotServe covers it.
func TestArm64DefaultFallsBackForFlagsSSACannotServe(t *testing.T) {
	dir := t.TempDir()
	flat := buildWith(t, dir, "arm64-linux", "flat", "flat")

	was := emitDebugSyms
	emitDebugSyms = true
	withG := buildWith(t, dir, "arm64-linux", "", "withg")
	emitDebugSyms = was
	// -g adds a symbol table, so the image differs from a plain flat build;
	// what matters is that it built at all and carries the debug symbols the
	// SSA backend refuses to emit.
	if len(withG) <= len(flat) {
		t.Errorf("-g on arm64-linux produced %d bytes against a plain build's %d: it did not fall back to the emitter that serves -g", len(withG), len(flat))
	}

	wasCover := ast.CoverEnabled
	ast.CoverEnabled = true
	cover := buildWith(t, dir, "arm64-linux", "", "cover")
	ast.CoverEnabled = wasCover
	if len(cover) == 0 {
		t.Error("-cover on arm64-linux produced nothing")
	}
}

// A name nothing implements is an error, not a silent fallback to the default.
func TestUnknownBackendIsRefused(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, err := run(src, filepath.Join(dir, "p"), "x86-64-linux", "stack", "", "", false, true, "", false, false, false, nil, false, "", false, nil)
	if err == nil || code == 0 {
		t.Fatalf("-backend stack built something instead of being refused: code=%d err=%v", code, err)
	}
}

// The arm64-linux DEFAULT build carries unwind data.
//
// TestSSABackendsCarryUnwindData proves the SSA emitter does, but it names
// `-backend ssa`, and TestNativeLinkPlacesEhFrame builds x86-64-linux. Neither
// covers what a plain `fern -target arm64-linux` now produces, and that is the
// build this change alters. A gate that checks a backend by name, while the
// default moves under it, is how both SSA backends came to ship with no
// .eh_frame at all in the first place (#9495, #9500).
func TestArm64DefaultBuildCarriesUnwindData(t *testing.T) {
	dir := t.TempDir()
	img := buildWith(t, dir, "arm64-linux", "", "dflt")

	off, ok := findCIE(img)
	if !ok {
		t.Fatal("no .eh_frame CIE in a default arm64-linux build: nothing could walk out of a frame in the binary this target now ships")
	}
	if got, want := string(img[off+8:off+16]), "\x01zR\x00\x04\x78\x1e\x01"; got != want {
		t.Errorf("CIE header is % x, want % x — the aarch64 profile", got, want)
	}
	codeOff, codeSz, ok := firstLoad(img)
	if !ok {
		t.Fatal("no PT_LOAD in the image")
	}
	if uint64(off) < codeOff || uint64(off) >= codeOff+codeSz {
		t.Errorf(".eh_frame at %#x is outside the R+X segment [%#x, %#x)", off, codeOff, codeOff+codeSz)
	}
	hdrOff, hdrSz, ok := ehFrameHdrSeg(img)
	if !ok {
		t.Fatal("no PT_GNU_EH_FRAME in a default arm64-linux build: a backtrace would stop at the first frame")
	}
	if n := int(binary.LittleEndian.Uint32(img[hdrOff+8:])); n == 0 {
		t.Error("the search table is empty, so the image carries FDEs nothing can find")
	} else if want := uint64(12 + 8*n); hdrSz != want {
		t.Errorf(".eh_frame_hdr is %d bytes for %d FDEs, want %d", hdrSz, n, want)
	}
}
