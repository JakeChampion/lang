package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

const snapshotLoanPrelude = `import "std/pmap";
import "std/pvec";
struct H { fd: i32 }
function (own h: H) close(): i32 { return h.fd; }
function take(own xs: H[]): i32 { return xs.len(); }
function take_entries(own xs: (i32, H)[]): i32 { return xs.len(); }
function take_keys(own xs: H[]): i32 { return xs.len(); }
function take_data(own xs: i32[]): i32 { return xs.len(); }
function take_pairs(own xs: (i32, i32)[]): i32 { return xs.len(); }
`

// A snapshot of a borrowed persistent collection holds the elements of
// that collection, so its resource elements stay borrowed.
func TestPersistentSnapshotsOfABorrowKeepTheLoan(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"pmap-values", `function f(m: pmap.PMap[i32, H]): i32 { return take(m.values()); }`, "E051"},
		{"pmap-keys", `function f(m: pmap.PMap[H, i32]): i32 { return take_keys(m.keys()); }`, "E051"},
		{"pmap-entries", `function f(m: pmap.PMap[i32, H]): i32 { return take_entries(m.entries()); }`, "E051"},
		{"pvec-to-array", `function f(v: pvec.PVec[H]): i32 { return take(v.to_array()); }`, "E051"},
		{"pmap-data-values", `function f(m: pmap.PMap[i32, i32]): i32 { return take_data(m.values()); }`, ""},
		{"pmap-data-keys", `function f(m: pmap.PMap[i32, i32]): i32 { return take_data(m.keys()); }`, ""},
		{"pmap-data-entries", `function f(m: pmap.PMap[i32, i32]): i32 { return take_pairs(m.entries()); }`, ""},
		{"pmap-values-read", `function f(m: pmap.PMap[i32, H]): i32 { return m.values().len(); }`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			program, _, err := modload.LoadSource(snapshotLoanPrelude + tc.body + "\nfunction main(): i32 { return 0; }\n")
			if err != nil {
				t.Fatal(err)
			}
			_, err = Check(program)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want no error, got %v", err)
				}
				return
			}
			if !hasCode(err, tc.want) {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}
