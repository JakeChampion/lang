package checker_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

// `IoError.Other` carries (path, message, errno), the errno an i32 in Linux
// numbering, and std/errno names it (#11296).
func TestIoErrorOtherCarriesAnErrno(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string // "" = must type-check
	}{
		{"three payloads", `function main(): i32 { let e: IoError = Other("p", "m", 21); match (e) { Other(p, m, n) => { return n; }, _ => { return 0; } } }`, ""},
		{"two arguments", `function main(): i32 { let e: IoError = Other("p", "m"); return 0; }`, "takes 3 arguments but was given 2"},
		{"errno is an i32", `function main(): i32 { let e: IoError = Other("p", "m", "21"); return 0; }`, "payload 2 type string, expected i32"},
		{"two bindings", `function main(): i32 { let e: IoError = Unsupported; match (e) { Other(p, m) => { return 1; }, _ => { return 0; } } }`, "carries 3 values, but the pattern binds 2"},
		{"errno binding is an i32", `function main(): i32 { let e: IoError = Unsupported; match (e) { Other(p, m, n) => { let s: string = n; return 1; }, _ => { return 0; } } }`, "got i32"},
		{"std/errno names it", "import \"std/errno\";\nfunction main(): i32 { let n: i32 = errno.of(Other(\"p\", \"m\", errno.ENOTDIR)); return n - errno.ENOTDIR + errno.of(NotFound(\"p\")) - errno.ENOENT; }", ""},
		{"errno.of takes an IoError", "import \"std/errno\";\nfunction main(): i32 { return errno.of(2); }", "IoError"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource(tc.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := constfold.Fold(prog, nil); err != nil {
				t.Fatalf("constfold: %v", err)
			}
			_, err = checker.Check(prog)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("check: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("check accepted %s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("check error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
