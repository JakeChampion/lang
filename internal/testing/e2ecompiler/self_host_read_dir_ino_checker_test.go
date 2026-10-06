package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// TestSelfHostReadDirInoChecker types read_dir_ino's signature and DirEntry's
// fields through the self-host checker and native's, which must give the same
// verdict and code for each program.
func TestSelfHostReadDirInoChecker(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_run.fern", "checker_run")
	for _, tc := range []struct{ name, src, code string }{
		{"fields", `function main(): i32 {
    match (read_dir_ino(".")) {
        Ok(es) => { let e: DirEntry = es[0]; let n: string = e.name; let ino: i64 = e.ino; return n.len(); },
        Err(_) => { return 1; },
    }
}`, ""},
		{"ino is i64", `function main(): i32 { match (read_dir_ino(".")) { Ok(es) => { let n: i32 = es[0].ino; return n; }, Err(_) => { return 1; } } }`, "E003"},
		{"entries are not names", `function main(): i32 { match (read_dir_ino(".")) { Ok(es) => { let s: string[] = es; return 0; }, Err(_) => { return 1; } } }`, "E003"},
		{"path is a string", `function main(): i32 { match (read_dir_ino(1)) { Ok(_) => { return 0; }, Err(_) => { return 1; } } }`, "E038"},
		{"DirEntry is reserved", "struct DirEntry { name: string }\nfunction main(): i32 { return 0; }", "E010"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = checker.Check(prog)
			if tc.code == "" && err != nil || tc.code != "" && (err == nil || !strings.Contains(diag.Format("read_dir_ino.fern", tc.src, err), tc.code)) {
				t.Fatalf("native: want code %q, got %v", tc.code, err)
			}
			code, stderr := runSelfHostChecker(t, bin, runner, tc.src)
			if tc.code == "" {
				if code != 0 || strings.TrimSpace(stderr) != "" {
					t.Fatalf("self-host rejected: exit %d\n%s", code, stderr)
				}
			} else if code == 0 || !strings.Contains(stderr, tc.code) {
				t.Fatalf("self-host: want %s, got exit %d\n%s", tc.code, code, stderr)
			}
		})
	}
}
