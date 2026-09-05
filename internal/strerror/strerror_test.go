package strerror

import (
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// hostErrnos is the host libc's name → number map, one file per OS, so
// the numbers in Table are checked against the kernel's own constants
// on whichever OS the suite runs.

func TestNumbersMatchHost(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Table {
		want, ok := hostErrnos[e.Name]
		if !ok {
			t.Errorf("%s: no host constant listed in hostErrnos for %s", e.Name, runtime.GOOS)
			continue
		}
		seen[e.Name] = true
		if got := e.Number(runtime.GOOS); got != int(want) {
			t.Errorf("%s: Table says %d on %s, the host says %d", e.Name, got, runtime.GOOS, int(want))
		}
	}
	for name := range hostErrnos {
		if !seen[name] {
			t.Errorf("hostErrnos lists %s, which Table does not carry", name)
		}
	}
}

// Go's Linux errno strings are glibc's text lower-cased, so on Linux
// the wording itself can be checked against the host, not just the
// numbers.
func TestTextMatchesGlibc(t *testing.T) {
	if runtime.GOOS != Linux {
		t.Skipf("Go's %s errno strings are the host libc's wording, not glibc's; only the numbers are checked there", runtime.GOOS)
	}
	for _, e := range Table {
		if e.Linux == 0 {
			continue
		}
		if got, want := syscall.Errno(e.Linux).Error(), strings.ToLower(e.Text); got != want {
			t.Errorf("%s (%d): Table says %q, glibc says %q", e.Name, e.Linux, e.Text, got)
		}
	}
}

func TestNumbersUniquePerOS(t *testing.T) {
	for _, os := range []string{Linux, Darwin, Wasi} {
		byNumber := map[int]string{}
		for _, e := range Table {
			n := e.Number(os)
			if n == 0 {
				continue
			}
			if prev, dup := byNumber[n]; dup {
				t.Errorf("%s: errno %d is both %s and %s", os, n, prev, e.Name)
			}
			byNumber[n] = e.Name
		}
	}
}

func TestTextAndDense(t *testing.T) {
	if got := Text(Linux, 9); got != "Bad file descriptor" {
		t.Errorf("Text(linux, 9) = %q", got)
	}
	if got := Text(Darwin, 21); got != "Is a directory" {
		t.Errorf("Text(darwin, 21) = %q", got)
	}
	if got := Text(Wasi, 54); got != "Not a directory" {
		t.Errorf("Text(wasi, 54) = %q", got)
	}
	if got := Text(Linux, 999); got != "Unknown error 999" {
		t.Errorf("Text(linux, 999) = %q", got)
	}
	if got := Text(Linux, 0); got != "Unknown error 0" {
		t.Errorf("Text(linux, 0) = %q — errno 0 must not match an entry that has no Linux number", got)
	}
	for _, os := range []string{Linux, Darwin, Wasi} {
		dense := Dense(os)
		for n, text := range dense {
			if want := Text(os, n); text != "" && text != want {
				t.Errorf("Dense(%s)[%d] = %q, Text = %q", os, n, text, want)
			} else if text == "" && !strings.HasPrefix(want, "Unknown error ") {
				t.Errorf("Dense(%s)[%d] is empty but Text = %q", os, n, want)
			}
		}
		if last := dense[len(dense)-1]; last == "" {
			t.Errorf("Dense(%s) ends in an empty slot; it should end at the largest errno", os)
		}
	}
}

func TestWasiErrorCodes(t *testing.T) {
	// The component package carries the enum as the WIT declares it; a
	// change there is a change to this list, in the same order.
	if len(WasiErrorCodes) != len(component.WasiFilesystemErrorCodeNames) {
		t.Fatalf("WasiErrorCodes has %d entries, component.WasiFilesystemErrorCodeNames has %d",
			len(WasiErrorCodes), len(component.WasiFilesystemErrorCodeNames))
	}
	for i, ec := range WasiErrorCodes {
		if want := component.WasiFilesystemErrorCodeNames[i]; ec.Code != want {
			t.Errorf("error-code %d is %q here, %q in the component package", i, ec.Code, want)
		}
		if Number(Wasi, ec.Errno) == 0 {
			t.Errorf("error-code %d (%s) names %s, which has no WASI number in Table", i, ec.Code, ec.Errno)
		}
	}
	// The four the translation always carried, at the discriminants the
	// runtime has matched since preview 2 landed.
	for _, tc := range []struct{ code, errno int }{{0, 2}, {7, 20}, {11, 27}, {27, 58}, {14, 31}, {24, 54}, {20, 44}} {
		if got := WasiErrnoOfCode(tc.code); got != tc.errno {
			t.Errorf("WasiErrnoOfCode(%d) = %d, want %d", tc.code, got, tc.errno)
		}
	}
	if got := WasiErrnoOfCode(37); got != 44 {
		t.Errorf("WasiErrnoOfCode(37) = %d, want ENOENT (44) for a discriminant past the enum", got)
	}
}
