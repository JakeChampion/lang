package e2ecompiler

import "testing"

func TestSelfHostProvidedCorpusVerdict(t *testing.T) {
	const clean = "irverifyprovided: clean (checked 3 functions, 4 direct calls)\n"
	const dirty = "irverifyprovided: 1 problem(s) (checked 3 functions, 4 direct calls)\n  unknown callee\n"
	for _, tc := range []struct {
		name  string
		exit  int
		out   string
		calls int
		bad   bool
	}{
		{"clean", 0, clean, 4, false},
		{"zero calls", 0, "irverifyprovided: clean (checked 0 functions, 0 direct calls)\n", 0, false},
		{"diagnostic", 1, dirty, 0, true},
		{"arena exhausted", 125, dirty, 0, true},
		{"killed wrapper", 137, "", 0, true},
		{"signalled driver", -1, "", 0, true},
		{"other error", 1, "could not read entry file\n", 0, true},
		{"empty output", 0, "", 0, true},
		{"exit one clean header", 1, clean, 0, true},
		{"exit zero dirty header", 0, dirty, 0, true},
		{"negative count", 0, "irverifyprovided: clean (checked -1 functions, 4 direct calls)\n", 0, true},
		{"overflow", 0, "irverifyprovided: clean (checked 3 functions, 9999999999999999999999999 direct calls)\n", 0, true},
		{"truncated header", 0, "irverifyprovided: clean (checked 3 functions, 4 direct calls", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, err := validateProvidedCorpusVerdict(tc.exit, tc.out)
			if (err != nil) != tc.bad || calls != tc.calls {
				t.Fatalf("verdict = %d, %v; want calls=%d bad=%t", calls, err, tc.calls, tc.bad)
			}
		})
	}
}
