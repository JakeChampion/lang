package e2eselfhost

import "testing"

func TestSelfHostProvidedCorpusVerdict(t *testing.T) {
	const clean = "irverifyprovided: clean (checked 3 functions, 4 direct calls)\n"
	const dirty = "irverifyprovided: 1 problem(s) (checked 3 functions, 4 direct calls)\n  unknown callee\n"
	for _, tc := range []struct {
		name          string
		expectedDirty bool
		exit          int
		out           string
		calls         int
		bad           bool
	}{
		{"clean", false, 0, clean, 4, false},
		{"expected diagnostic", true, 1, dirty, 4, false},
		{"zero calls", false, 0, "irverifyprovided: clean (checked 0 functions, 0 direct calls)\n", 0, false},
		{"unexpected diagnostic", false, 1, dirty, 0, true},
		{"unexpected clean", true, 0, clean, 0, true},
		{"arena exhausted", true, 125, dirty, 0, true},
		{"killed wrapper", true, 137, "", 0, true},
		{"signalled driver", true, -1, "", 0, true},
		{"other error", true, 1, "could not read entry file\n", 0, true},
		{"empty output", false, 0, "", 0, true},
		{"exit one clean header", true, 1, clean, 0, true},
		{"exit zero dirty header", false, 0, dirty, 0, true},
		{"negative count", false, 0, "irverifyprovided: clean (checked -1 functions, 4 direct calls)\n", 0, true},
		{"overflow", false, 0, "irverifyprovided: clean (checked 3 functions, 9999999999999999999999999 direct calls)\n", 0, true},
		{"truncated header", false, 0, "irverifyprovided: clean (checked 3 functions, 4 direct calls", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, err := validateProvidedCorpusVerdict(tc.expectedDirty, tc.exit, tc.out)
			if (err != nil) != tc.bad || calls != tc.calls {
				t.Fatalf("verdict = %d, %v; want calls=%d bad=%t", calls, err, tc.calls, tc.bad)
			}
		})
	}
}
