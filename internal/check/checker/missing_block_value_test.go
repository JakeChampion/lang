package checker

import "testing"

func TestMissingBlockValueHasNoBranchTypeCascade(t *testing.T) {
	cases := []struct {
		name, source, codes string
	}{
		{"if", `function main(): i32 { let x = if (true) { print("x"); } else { 0 }; return 0; }`, "E061"},
		{"literal-match", `function main(): i32 { let x = match (1) { 0 => { print("x"); }, _ => { 0 } }; return 0; }`, "E061"},
		{"enum-match", `enum Choice { First, Second } function main(): i32 { let c = First; let x = match (c) { First => { print("x"); }, Second => { 0 } }; return 0; }`, "E061"},
		{"if-let", `function main(): i32 { let x = if let Some(n) = Some(1) { print("x"); } else { 0 }; return 0; }`, "E061"},
		{"real-branch-mismatch", `function main(): i32 { let x = if (true) { "x" } else { 0 }; return 0; }`, "E031"},
		{"real-void-tail", `function main(): i32 { let x = if (true) { print("x") } else { 0 }; return 0; }`, "E031"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.source)
			if got := errCodes(err); got != tc.codes {
				t.Fatalf("diagnostic codes = %q, want %q\n%v", got, tc.codes, err)
			}
		})
	}
}
