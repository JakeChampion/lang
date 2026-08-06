package modload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWithLib writes `src` as the entry and `lib` as a sibling `lib.fern`,
// then loads the entry. Returns the load error, which is where the
// visibility and undeclared-name diagnostics surface.
func loadWithLib(t *testing.T, src, lib string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lib.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}
	entry := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	_, _, err := Load(entry)
	return err
}

// A `mod.X` reference has two distinct failure modes and they used to share
// one message. `X` may be declared in `mod` without `pub` — in which case
// "is not exported (declare it as `pub …`)" is exactly right — or `mod` may
// have no `X` at all, in which case that message sends the reader off to add
// `pub` to a declaration that does not exist.
//
// For a qualified TYPE it was also the ONLY error produced: nothing
// downstream reports the unknown name, so a typo in `rand.Rnd` and a
// visibility mistake on `rand.Rand` were indistinguishable. That is how this
// was found — guessing a `std/rand` type that does not exist and being told
// to go export it.
func TestUndeclaredNameIsNotReportedAsUnexported(t *testing.T) {
	lib := `struct Hidden { v: i32 }
function hidden_fn(): i32 { return 1; }
const HIDDEN_C: i32 = 3;
pub struct Shown { v: i32 }
pub function shown_fn(): i32 { return 1; }
`
	cases := []struct {
		name       string
		src        string
		wantSubstr string
		notSubstr  string
	}{
		{
			name:       "undeclared type",
			src:        `import "./lib";` + "\n" + `function main(): i32 { var q: lib.NoSuchType = 0; return 0; }`,
			wantSubstr: `module "lib" has no type "NoSuchType"`,
			notSubstr:  "is not exported",
		},
		{
			name:       "undeclared function",
			src:        `import "./lib";` + "\n" + `function main(): i32 { return lib.no_such_fn(); }`,
			wantSubstr: `module "lib" has no function "no_such_fn"`,
			notSubstr:  "is not exported",
		},
		{
			name:       "private type still reports not exported",
			src:        `import "./lib";` + "\n" + `function main(): i32 { var q: lib.Hidden = 0; return 0; }`,
			wantSubstr: "lib.Hidden is not exported",
			notSubstr:  "has no type",
		},
		{
			name:       "private function still reports not exported",
			src:        `import "./lib";` + "\n" + `function main(): i32 { return lib.hidden_fn(); }`,
			wantSubstr: "lib.hidden_fn is not exported",
			notSubstr:  "has no function",
		},
		{
			name:       "private const still reports not exported",
			src:        `import "./lib";` + "\n" + `function main(): i32 { return lib.HIDDEN_C; }`,
			wantSubstr: "lib.HIDDEN_C is not exported",
			notSubstr:  "has no",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithLib(t, tc.src, lib)
			if err == nil {
				t.Fatalf("expected an error")
			}
			got := err.Error()
			if !strings.Contains(got, tc.wantSubstr) {
				t.Fatalf("wanted %q in:\n%s", tc.wantSubstr, got)
			}
			if strings.Contains(got, tc.notSubstr) {
				t.Fatalf("did not want %q in:\n%s", tc.notSubstr, got)
			}
		})
	}
}

// A near-miss on an exported name gets a suggestion. The threshold is tight
// on purpose: a wrong suggestion on a name the reader typed correctly costs
// more than no suggestion, so a name that resembles nothing gets none.
func TestUndeclaredNameSuggestsAnExportedNeighbour(t *testing.T) {
	lib := `pub function shown_fn(): i32 { return 1; }
pub struct Shown { v: i32 }
`
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "near-miss function",
			src:  `import "./lib";` + "\n" + `function main(): i32 { return lib.shown_fnn(); }`,
			want: `did you mean "shown_fn"?`,
		},
		{
			name: "near-miss type",
			src:  `import "./lib";` + "\n" + `function main(): i32 { var q: lib.Shwon = 0; return 0; }`,
			want: `did you mean "Shown"?`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithLib(t, tc.src, lib)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q in:\n%s", tc.want, err.Error())
			}
		})
	}
}

// Nothing close enough gets no suggestion — an unrelated name must not be
// dressed up as a typo.
func TestNoSuggestionWhenNothingIsClose(t *testing.T) {
	lib := `pub function shown_fn(): i32 { return 1; }` + "\n"
	err := loadWithLib(t, `import "./lib";`+"\n"+`function main(): i32 { return lib.zzzzzzqqqqq(); }`, lib)
	if err == nil {
		t.Fatalf("expected an error")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("suggested a neighbour for an unrelated name:\n%s", err.Error())
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"rng_sed", "rng_seed", 1},
		{"Shwon", "Shown", 2},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, tc := range cases {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Fatalf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
