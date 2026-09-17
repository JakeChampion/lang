package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
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

// Every target defaults to the stack-machine emitter. The SSA backend is
// selected by name only: it emits less code, but on the single-word string ABI
// it runs, a string passed to a user function is never reclaimed, so retention
// grows with the input (internal/e2e/arm64_default_string_reclaim_test.go).
//
// Byte-identity is the assertion in both directions, so this fails if a target
// silently changes which emitter it gets.
func TestDefaultBackendPerTarget(t *testing.T) {
	for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			dflt := buildWith(t, dir, target, "", "dflt")
			flat := buildWith(t, dir, target, "flat", "flat")
			if !bytes.Equal(dflt, flat) {
				t.Errorf("the default for %s is not -backend flat: %d bytes against %d", target, len(dflt), len(flat))
			}
			// And the two names genuinely reach different emitters on the
			// targets that have both — otherwise the check above proves
			// nothing about which one ran.
			if target == "wasm32-wasi" {
				return
			}
			if ssa := buildWith(t, dir, target, "ssa", "ssa"); bytes.Equal(dflt, ssa) {
				t.Errorf("-backend flat and -backend ssa produced identical images on %s, so this test cannot tell them apart", target)
			}
		})
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

// resolveBackend returns what the caller named, and the stack-machine emitter
// otherwise. There is no per-flag fallback any more: the SSA backend is not
// any target's default, so there is nothing to fall back FROM, and a build
// that names it alongside a flag it cannot serve is refused by
// ssaUnservedFlag rather than quietly re-resolved.
func TestResolveBackendDefaultsToFlatOnEveryTarget(t *testing.T) {
	for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi", "arm64-darwin"} {
		if got := resolveBackend("", target, false, "", "", false); got != "flat" {
			t.Errorf("resolveBackend(%q) = %q, want \"flat\"", target, got)
		}
	}
	// A named backend is never second-guessed: the caller said which emitter,
	// and ssaUnservedFlag is what refuses a combination it cannot serve.
	if got := resolveBackend("ssa", "arm64-linux", true, "gcc", "add", false); got != "ssa" {
		t.Errorf("an explicit -backend ssa resolved to %q", got)
	}
}

// The predicate above is only half the fix; this proves the wiring reaches
// it. `-cc /bin/false` cannot link, so a build that honours -cc fails — and
// one that silently links in-process instead reports success and writes a
// binary, which is what the flip did before this.
func TestArm64DefaultHonoursExternalCC(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte(defaultBackendProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "p")
	// native=false, as the CLI passes unless -native is given: `useNative`
	// otherwise forces the in-process link and cc never gets a say.
	code, err := run(src, out, "arm64-linux", "", "", "/bin/false", false, false, "", false, false, false, nil, false, "", false, nil)
	if err == nil && code == 0 {
		t.Fatal("-cc /bin/false succeeded: the build linked in-process and ignored the external linker it was given")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("-cc /bin/false wrote a binary despite failing to link")
	}
}
