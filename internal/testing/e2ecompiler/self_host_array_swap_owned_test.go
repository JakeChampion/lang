package e2ecompiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostArraySwapOwnsReplacement(t *testing.T) {
	data, err := os.ReadFile(langSrcAbs(t, "internal/check/checker/testdata/array_swap_owned_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Source, Want string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.Source), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput()
			if tc.Want == "" {
				if err != nil {
					t.Fatalf("check: %v\n%s", err, out)
				}
				return
			}
			for _, code := range strings.Split(tc.Want, "|") {
				if err != nil && strings.Contains(string(out), "["+code+"]") {
					return
				}
			}
			t.Fatalf("check: %v, want %s\n%s", err, tc.Want, out)
		})
	}
}

func TestSelfHostArraySwapOwnedInputsRun(t *testing.T) {
	ownedResultRuntimeCase(t, "owned_array_exchange_inputs")
}
