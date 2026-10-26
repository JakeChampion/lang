package e2ecompiler

import (
	"bytes"
	"os/exec"
	"reflect"
	"testing"
)

func TestSelfHostArrayMatchPatterns(t *testing.T) {
	bin, runner, _ := buildCheckerCodesBin(t)
	for _, tc := range []struct {
		name, source string
		codes        []string
	}{
		{"owned-statement", `function f(xs: i32[]): i32 { match (xs) { Red => { return 1; }, _ => { return 0; } } }`, []string{"E035"}},
		{"view-statement", `function f(xs: [i32]): i32 { match (xs) { Red => { return 1; }, _ => { return 0; } } }`, []string{"E035"}},
		{"owned-value", `function f(xs: i32[]): i32 { return match (xs) { Red => 1, _ => 0 }; }`, []string{"E035"}},
		{"view-value", `function f(xs: [i32]): i32 { return match (xs) { Red => 1, _ => 0 }; }`, []string{"E035"}},
		{"wildcard", `function f(xs: i32[]): i32 { return match (xs) { _ => 0 }; }`, nil},
		{"enum", `function f(color: Color): i32 { return match (color) { Red => 1, Blue => 0 }; }`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := `enum Color { Red, Blue } ` + tc.source + ` function main(): i32 { return 0; }`
			if got := goCheckerDiagCodes(t, t.TempDir(), source); !reflect.DeepEqual(got, tc.codes) {
				t.Errorf("Go codes: got %v, want %v", got, tc.codes)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = bytes.NewBufferString(source)
			out := runCheckerDriver(t, cmd, tc.name)
			var codes []string
			for _, diagnostic := range driverDiags(out) {
				codes = append(codes, diagnostic.code)
			}
			if !reflect.DeepEqual(codes, tc.codes) {
				t.Errorf("self-host codes: got %v, want %v\n%s", codes, tc.codes, out)
			}
		})
	}
}
