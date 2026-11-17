package checker

import (
	"strings"
	"testing"
)

const lateGroupHead = "function main(): i32 {\n  function a(n: i32): i32 { if (n == 0) { return 0; } return b(n - 1); }\n"

func lateGroupB(result string) string {
	return "  function b(n: i32): i32 { if (n == 0) { return " + result + "; } return a(n - 1); }\n"
}

func TestGroupCaptureDeclaredAfterFirstFunctionIsE108(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"local", lateGroupHead + "  let k = 5;\n" + lateGroupB("k") + "  return a(3);\n}\n", []string{"k"}},
		{"destructured-local", lateGroupHead + "  let (k, j) = (5, 6);\n" + lateGroupB("k + j") + "  return a(3);\n}\n", []string{"k", "j"}},
		{"function", lateGroupHead + "  function h(n: i32): i32 { return n + 5; }\n" + lateGroupB("h(0)") + "  return a(3);\n}\n", []string{"h"}},
		{"function-of-a-second-group", lateGroupHead + "  function c(n: i32): i32 { if (n == 0) { return 1; } return d(n - 1); }\n  function d(n: i32): i32 { if (n == 0) { return 2; } return c(n - 1); }\n" + lateGroupB("c(1) + c(2)") + "  return a(3);\n}\n", []string{"c"}},
		{"in-a-lambda", lateGroupHead + "  let k = 5;\n  function b(n: i32): i32 { let g = (): i32 => k; if (n == 0) { return g(); } return a(n - 1); }\n  return a(3);\n}\n", []string{"k"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkErrors(t, tc.src)
			if len(errs) != len(tc.want) {
				t.Fatalf("got %v, want %d E108", errs, len(tc.want))
			}
			for i, name := range tc.want {
				want := `nested function "b" captures "` + name + `", but "a" can call "b" before the declaration of "` + name + `": declare "` + name + `" before "a"`
				if errs[i].ErrCode != "E108" || !strings.Contains(errs[i].Msg, want) {
					t.Errorf("got %v, want E108 with %q", errs[i], want)
				}
			}
		})
	}
}

func TestGroupCaptureDeclaredInTimeIsAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"before-the-first-function": "function main(): i32 {\n  let k = 5;\n  function a(n: i32): i32 { if (n == 0) { return 0; } return b(n - 1); }\n" + lateGroupB("k") + "  return a(3);\n}\n",
		"no-function-reads-it":      lateGroupHead + "  let k = 5;\n" + lateGroupB("n") + "  return a(3) + k;\n}\n",
		"the-function-declares-it":  lateGroupHead + "  let k = 5;\n  function b(n: i32): i32 { let k = 2; if (n == 0) { return k; } return a(n - 1); }\n  return a(3) + k;\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if errs := checkErrors(t, src); len(errs) != 0 {
				t.Fatalf("got %v, want no error", errs)
			}
		})
	}
}
