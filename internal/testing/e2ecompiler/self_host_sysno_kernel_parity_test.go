package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The self-host's syscall numbers are hand-written tables in asmcore.fern's
// `sysno`, and Go's zsysnum files are a second transcription of the same
// kernel sources. Comparing them catches a TRANSPOSITION, the one error in a
// table no behavioural test can see: fsync(2) and fdatasync(2) have identical
// error contracts, so a program cannot tell which of the two ran, and their
// Linux numbers are adjacent, which is exactly when a transposition happens.
//
// The oracle is read from GOROOT's source rather than taken from
// `syscall.SYS_*`, which only holds the host's GOARCH, so every table is
// checked on every host.

const sysnoSrc = "../../../compiler/asmcore.fern"

// sysnoAliases maps a self-host row to the kernel name whose number it
// carries, where the two differ on purpose (asmcore.fern says why at each
// row). Go's tables stop before faccessat2, so those rows go unchecked.
var sysnoAliases = map[string]map[string]string{
	"sysno_x86_64": {
		"getdents":  "getdents64",
		"faccessat": "faccessat2",
	},
	"sysno_arm64_linux": {
		"getdents":  "getdents64",
		"faccessat": "faccessat2",
	},
	"sysno_arm64_darwin": {
		"fstat":     "fstat64",
		"getrandom": "getentropy",
		"statfs":    "statfs64",
	},
}

// fernSysnoTable reads the `name -> number` rows of the `sysno_*` function fn.
func fernSysnoTable(t *testing.T, src, fn string) map[string]int {
	t.Helper()
	head := "function " + fn + "(name: string): string {"
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("no `%s` in %s: the extraction has gone stale, which would make this test vacuous", head, sysnoSrc)
	}
	body := src[i+len(head):]
	end := strings.Index(body, `return "-1";`)
	if end < 0 {
		t.Fatalf("`%s` has no `return \"-1\";` miss: the table layout changed", fn)
	}
	row := regexp.MustCompile(`name == "([a-z0-9_]+)"\)\s*\{\s*return "(-?\d+)";\s*\}`)
	out := map[string]int{}
	for _, m := range row.FindAllStringSubmatch(body[:end], -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("%s row %q: %v", fn, m[1], err)
		}
		out[m[1]] = n
	}
	return out
}

// goSysnoTable reads the SYS_* constants of GOROOT's zsysnum file `file`,
// keyed by lower-case name.
func goSysnoTable(t *testing.T, file string) map[string]int {
	t.Helper()
	path := filepath.Join(runtime.GOROOT(), "src", "syscall", file)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the oracle %s: %v", path, err)
	}
	out := map[string]int{}
	for _, m := range regexp.MustCompile(`(?m)^\s*SYS_([A-Z0-9_]+)\s*=\s*(\d+)`).FindAllStringSubmatch(string(b), -1) {
		n, _ := strconv.Atoi(m[2])
		out[strings.ToLower(m[1])] = n
	}
	return out
}

func TestSelfHostSysnoMatchesTheKernelTables(t *testing.T) {
	b, err := os.ReadFile(sysnoSrc)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, c := range []struct{ fn, oracle string }{
		{"sysno_x86_64", "zsysnum_linux_amd64.go"},
		{"sysno_arm64_linux", "zsysnum_linux_arm64.go"},
		{"sysno_arm64_darwin", "zsysnum_darwin_arm64.go"},
	} {
		t.Run(c.fn, func(t *testing.T) {
			fern, kernel := fernSysnoTable(t, src, c.fn), goSysnoTable(t, c.oracle)
			// Anchors: a table that stopped matching the row pattern would
			// otherwise pass on an empty intersection.
			for _, name := range []string{"read", "write", "fsync", "fdatasync"} {
				if _, ok := fern[name]; !ok {
					t.Fatalf("no `%s` row read from %s: the extraction is reading the wrong block", name, c.fn)
				}
			}
			compared := 0
			for name, got := range fern {
				kname := name
				if alias, ok := sysnoAliases[c.fn][name]; ok {
					kname = alias
				}
				want, ok := kernel[kname]
				if !ok {
					continue
				}
				compared++
				if got != want {
					t.Errorf("%s `%s` = %d, the kernel table says %d", c.fn, name, got, want)
				}
			}
			if compared < len(fern)/2 {
				t.Errorf("only %d of %d rows have a kernel counterpart in %s: the oracle is not the table it should be", compared, len(fern), c.oracle)
			}
		})
	}
}

// TestSelfHostSysnoLinuxTablesPairUp pins that the two Linux tables name the
// same calls. `sysno` answers a name it has no row for with "-1", which the
// emit splices in as a real syscall number: the kernel answers ENOSYS, the
// helper returns as if it had worked, and only the missing effect shows
// (#8827). The exceptions are the calls arm64 does not have, which its
// table spells as their replacements.
func TestSelfHostSysnoLinuxTablesPairUp(t *testing.T) {
	b, err := os.ReadFile(sysnoSrc)
	if err != nil {
		t.Fatal(err)
	}
	x86, arm := fernSysnoTable(t, string(b), "sysno_x86_64"), fernSysnoTable(t, string(b), "sysno_arm64_linux")
	x86Only := map[string]bool{"fork": true, "poll": true}
	armOnly := map[string]bool{"clone": true, "ppoll": true}
	for name := range x86 {
		if _, ok := arm[name]; !ok && !x86Only[name] {
			t.Errorf("sysno_arm64_linux has no `%s` row: the arm64 emit would issue -1 for it", name)
		}
	}
	for name := range arm {
		if _, ok := x86[name]; !ok && !armOnly[name] {
			t.Errorf("sysno_x86_64 has no `%s` row: the x86-64 emit would issue -1 for it", name)
		}
	}
}
