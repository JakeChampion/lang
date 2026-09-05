package strerror

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The self-host compiler carries its own copy of Table
// (examples/self_host/asmcore.fern) because it cannot import Go, the
// same way internal/platforms and internal/caps are mirrored. Two
// copies of an errno table go wrong the same way: one side gains an
// entry, or a Darwin number is corrected, and the other keeps reporting
// "Unknown error N" — silently, because neither compiler can see the
// other's list. This reads the Fern source as data and compares it row
// for row, without building anything, so the gate runs on every change
// to this package.

const selfHostSrc = "../../examples/self_host/asmcore.fern"

func readSelfHost(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(selfHostSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostSrc, err)
	}
	return string(b)
}

// fernListBody is the `[...]` body of `pub function NAME(): T[] { return [...]; }`.
func fernListBody(t *testing.T, src, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)pub function ` + name + `\(\): (?:string|i32)\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no %s() list found in %s — the extraction pattern has gone stale, which would make this test vacuous", name, selfHostSrc)
	}
	return m[1]
}

func fernStrings(t *testing.T, src, name string) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(fernListBody(t, src, name), -1) {
		out = append(out, m[1])
	}
	return out
}

func fernInts(t *testing.T, src, name string) []int {
	t.Helper()
	var out []int
	for _, m := range regexp.MustCompile(`-?\d+`).FindAllString(fernListBody(t, src, name), -1) {
		n, err := strconv.Atoi(m)
		if err != nil {
			t.Fatalf("%s(): %q is not an integer", name, m)
		}
		out = append(out, n)
	}
	return out
}

// TestSelfHostTableMatches pins the four parallel lists to Table, row
// for row and in order: the self-host generates its ladder by index, so
// a row out of place pairs a text with the wrong number.
func TestSelfHostTableMatches(t *testing.T) {
	src := readSelfHost(t)
	texts := fernStrings(t, src, "strerror_texts")
	lists := map[string][]int{
		Linux:  fernInts(t, src, "strerror_linux"),
		Darwin: fernInts(t, src, "strerror_darwin"),
		Wasi:   fernInts(t, src, "strerror_wasi"),
	}
	if len(texts) != len(Table) {
		t.Fatalf("strerror_texts() has %d rows, Table has %d — regenerate with `go run ./internal/strerror/gen_selfhost_lists`", len(texts), len(Table))
	}
	for os, nums := range lists {
		if len(nums) != len(Table) {
			t.Fatalf("strerror_%s() has %d rows, Table has %d — regenerate with `go run ./internal/strerror/gen_selfhost_lists`", os, len(nums), len(Table))
		}
	}
	for i, e := range Table {
		if texts[i] != e.Text {
			t.Errorf("row %d (%s): the self-host says %q, Table says %q", i, e.Name, texts[i], e.Text)
		}
		for os, nums := range lists {
			if nums[i] != e.Number(os) {
				t.Errorf("row %d (%s) on %s: the self-host says %d, Table says %d", i, e.Name, os, nums[i], e.Number(os))
			}
		}
	}
}

// TestSelfHostWasiErrorCodesMatch pins the preview-2 error-code
// translation the self-host wasm emitter bakes into
// $__fern_build_io_error_p2.
func TestSelfHostWasiErrorCodesMatch(t *testing.T) {
	got := fernInts(t, readSelfHost(t), "wasi_error_code_errnos")
	if len(got) != len(WasiErrorCodes) {
		t.Fatalf("wasi_error_code_errnos() has %d entries, WasiErrorCodes has %d — regenerate with `go run ./internal/strerror/gen_selfhost_lists`", len(got), len(WasiErrorCodes))
	}
	for i, ec := range WasiErrorCodes {
		if want := Number(Wasi, ec.Errno); got[i] != want {
			t.Errorf("error-code %d (%s): the self-host translates to %d, want %s = %d", i, ec.Code, got[i], ec.Errno, want)
		}
	}
}
