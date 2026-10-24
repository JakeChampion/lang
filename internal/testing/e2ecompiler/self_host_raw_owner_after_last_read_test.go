package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rawOwnerProbeSrc(work string, groups int) string {
	var sb strings.Builder
	sb.WriteString("function main(): i32 {\n    let bad: i32 = 0;\n")
	for n := 1; n <= 20; n++ {
		child := strings.Repeat("y", n)
		fmt.Fprintf(&sb, `    match (create_dir_all("%[1]s/t%[2]d/%[3]s")) { Ok(_) => {}, Err(e) => { return 8; } }
    match (remove_dir_all("%[1]s/t%[2]d")) { Ok(_) => {}, Err(e) => { return 9; } }
    match (stat("%[1]s/t%[2]d")) { Ok(_) => { eprint("left behind: t%[2]d/%[3]s\n"); bad = bad + 1; }, Err(e) => {} }
`, work, n, child)
	}
	for n := 1; n <= 80; n++ {
		fmt.Fprintf(&sb, `    match (read_file_bytes("%[1]s/f%[2]d")) { Ok(b) => { if (b.len() != %[2]d) { eprint("f%[2]d: length\n"); bad = bad + 1; } let i: i32 = 0; while (i < b.len()) { if ((b[i] as i32) != 65 + (i %% 26)) { eprint("f%[2]d: byte\n"); bad = bad + 1; i = b.len(); } i = i + 1; } }, Err(e) => { return 10; } }
`, work, n)
	}
	for n := 1; n <= 40; n++ {
		p := work + "/" + strings.Repeat("m", n)
		for _, call := range []string{"read_file", "open_reader", "remove_file"} {
			fmt.Fprintf(&sb, `    match (%[3]s("%[1]s")) { Ok(_) => { return 11; }, Err(e) => { match (e) { NotFound(p) => { if (p != "%[1]s") { eprint("%[3]s %[2]d: " + p + "\n"); bad = bad + 1; } }, _ => { return 12; } } } }
`, p, n, call)
		}
	}
	fmt.Fprintf(&sb, `    if (cpu_count() <= 0) { eprint("cpu_count answered 0\n"); bad = bad + 1; }
    let g: i64[] = getgroups();
    if (g.len() != %d) { eprint("getgroups answered the wrong count\n"); bad = bad + 1; }
    if (bad > 0) { return 13; }
    return 0;
}
`, groups)
	return sb.String()
}

func rawOwnerProbeWork(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	for n := 1; n <= 80; n++ {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(65 + i%26)
		}
		if err := os.WriteFile(filepath.Join(work, fmt.Sprintf("f%d", n)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return work
}

func TestSelfHostRawOwnerAfterLastRead(t *testing.T) {
	cli := buildSelfHostCLI(t)
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			src := rawOwnerProbeSrc(rawOwnerProbeWork(t), len(groups))
			stderr, code := cli.exitOf(t, src, target, "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d (8-12 = a call answered the wrong way, 13 = a body read a released block):\n%s", code, stderr)
			}
			allocs, frees, live := leakSummaryOf(t, target, stderr)
			if allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("allocs=%d frees=%d live_bytes=%d: an owner left a path without its release\n%s",
					allocs, frees, live, stderr)
			}
		})
	}
}
