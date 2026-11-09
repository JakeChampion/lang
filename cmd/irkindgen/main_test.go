package main

import (
	"os"
	"strings"
	"testing"
)

const irFern = "../../compiler/ir.fern"

func TestGeneratedFernIsUpToDate(t *testing.T) {
	src, err := os.ReadFile(irFern)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Rewrite(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if out != string(src) {
		t.Errorf("%s is out of date; regenerate with:\n\tgo run ./cmd/irkindgen %s", irFern, irFern)
	}
}

func TestMarkersArePresent(t *testing.T) {
	src, err := os.ReadFile(irFern)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{namesHead, begin, end} {
		if !strings.Contains(string(src), m) {
			t.Errorf("%s carries no %q, so Rewrite cannot keep KIND_ORDER in step", irFern, strings.TrimSpace(m))
		}
	}
}

func TestNamesRefusesABadList(t *testing.T) {
	for _, tc := range []struct{ name, list, want string }{
		{"wrong id", "  \"\",  // 0\n  \"add\",  // 2\n", "comment says 2"},
		{"duplicate", "  \"\",  // 0\n  \"add\",  // 1\n  \"add\",  // 2\n", "at ids 1 and 2"},
		{"bad line", "  \"\",  // 0\n  \"add\", // 1\n", "is not"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Names(namesHead + tc.list + namesTail)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Names: error %v, want one containing %q", err, tc.want)
			}
		})
	}
}
