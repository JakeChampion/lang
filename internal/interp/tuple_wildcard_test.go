package interp

import "testing"

func TestTupleMatchAcceptsWildcardOnlyArms(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"expression", `function main(): i32 { return match ((1i32, true)) { _ => 41i32 }; }`},
		{"statement", `function main(): i32 { match ((1i32, true)) { _ => { return 41i32; } } return 0; }`},
		{"expression-false-guard", `function main(): i32 {
  var value = 0i32;
  return match ((1i32, true)) { _ when { value = 41i32; false } => 0i32, _ => value };
}`},
		{"statement-false-guard", `function main(): i32 {
  var value = 0i32;
  match ((1i32, true)) { _ when { value = 41i32; false } => { return 0i32; }, _ => { return value; } }
  return 0i32;
}`},
		{"expression-true-guard", `function main(): i32 { return match ((1i32, true)) { _ when true => 41i32, _ => 0i32 }; }`},
		{"statement-true-guard", `function main(): i32 {
  match ((1i32, true)) { _ when true => { return 41i32; }, _ => { return 0i32; } } return 0i32;
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalProgramValue(t, tc.source); got != Number(41) {
				t.Fatalf("got %v, want 41", got)
			}
		})
	}
}
