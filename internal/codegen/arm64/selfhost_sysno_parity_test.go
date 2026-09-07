package arm64

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The self-host compiler carries its own arm64 syscall numbers in
// examples/self_host/asmcore.fern's `sysno`, because it cannot import Go —
// the same mirroring internal/platforms and internal/caps get. Two copies of
// a number table go wrong in one direction that nothing downstream can see:
// `sysno` answers a name it has no row for with "-1", and the self-host emit
// splices that straight into the helper as a real syscall number. The kernel
// answers ENOSYS, the helper returns as if it had worked, and the only
// observable is the effect that never happened.
//
// #8827 was exactly that. The arm64-linux table had no `sigaction` row, so
// the self-host build of tee(1) set neither disposition: `-i` did not ignore
// SIGINT and every `--output-error` mode still died of SIGPIPE, while the
// native build — reading the table below — got both right.
//
// This reads the Fern source as data and compares it row for row, so the gate
// costs nothing and runs on every change to this package.

const selfHostSysnoSrc = "../../../examples/self_host/asmcore.fern"

// handAsmOnly names the rows deliberately absent from `sysno`: the self-host
// arm64 backend writes these two as hand-assembly (the process exit path and
// the heap's initial mapping) rather than through a Fern runtime body, so
// they never reach `sysno` at all.
var handAsmOnly = map[string]bool{"exit_group": true, "mmap": true}

// selfHostSysnoTables returns `sysno`'s arm64-darwin and arm64-linux rows.
// The x86-64 rows are that backend's business and are skipped here.
func selfHostSysnoTables(t *testing.T) (darwin, linux map[string]int) {
	t.Helper()
	b, err := os.ReadFile(selfHostSysnoSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostSysnoSrc, err)
	}
	src := string(b)
	body := src[strings.Index(src, "pub function sysno("):]
	if !strings.HasPrefix(body, "pub function sysno(") {
		t.Fatalf("no `pub function sysno(` in %s — the extraction has gone stale, "+
			"which would make this test vacuous", selfHostSysnoSrc)
	}
	// Each per-target table ends in its own `return "-1";`, and the arm64
	// tables are the second and third: x86-64 first, then arm64-darwin, then
	// arm64-linux as the fall-through.
	miss := `return "-1";`
	cut := func(from int) (string, int) {
		end := strings.Index(body[from:], miss)
		if end < 0 {
			t.Fatalf("fewer than three `%s` misses in sysno — the table layout changed", miss)
		}
		return body[from : from+end], from + end + len(miss)
	}
	_, next := cut(0)            // x86-64
	darwinSrc, next := cut(next) // arm64-darwin
	linuxSrc, _ := cut(next)     // arm64-linux (the fall-through)
	row := regexp.MustCompile(`name == "([a-z0-9_]+)"\) \{ return "(-?\d+)"; \}`)
	read := func(what, s string) map[string]int {
		out := map[string]int{}
		for _, m := range row.FindAllStringSubmatch(s, -1) {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				t.Fatalf("%s row %q: %v", what, m[1], err)
			}
			out[m[1]] = n
		}
		if len(out) == 0 {
			t.Fatalf("no %s rows matched — the row pattern has gone stale", what)
		}
		return out
	}
	return read("arm64-darwin", darwinSrc), read("arm64-linux", linuxSrc)
}

func TestSelfHostArm64SysnoAgreesWithNative(t *testing.T) {
	darwin, linux := selfHostSysnoTables(t)
	// Anchors: a table that silently stopped matching would otherwise make
	// every check below pass on an empty intersection.
	if linux["read"] != sysRead || darwin["read"] != darRead {
		t.Fatalf("the `read` row does not match on either side (linux %d, darwin %d) — "+
			"the extraction is reading the wrong blocks", linux["read"], darwin["read"])
	}
	for name, pair := range linuxDarwinSysno {
		if handAsmOnly[name] {
			if _, ok := linux[name]; ok {
				t.Errorf("`%s` now has a sysno row — drop it from handAsmOnly and let the check below cover it", name)
			}
			continue
		}
		got, ok := linux[name]
		if !ok {
			t.Errorf("sysno has no arm64-linux `%s` row: the self-host emit would issue -1 for it", name)
		} else if got != pair[0] {
			t.Errorf("arm64-linux `%s`: sysno says %d, this backend says %d", name, got, pair[0])
		}
		got, ok = darwin[name]
		if !ok {
			t.Errorf("sysno has no arm64-darwin `%s` row: the self-host emit would issue -1 for it", name)
		} else if got != pair[1] {
			t.Errorf("arm64-darwin `%s`: sysno says %d, this backend says %d", name, got, pair[1])
		}
	}
	// The Linux-only half. Their Darwin column is deliberately different in
	// kind rather than in number — getrandom's arm64-darwin row is
	// getentropy, which is why only the Linux number is compared.
	for name, want := range linuxOnlySysno {
		got, ok := linux[name]
		if !ok {
			t.Errorf("sysno has no arm64-linux `%s` row: the self-host emit would issue -1 for it", name)
		} else if got != want {
			t.Errorf("arm64-linux `%s`: sysno says %d, this backend says %d", name, got, want)
		}
	}
}
