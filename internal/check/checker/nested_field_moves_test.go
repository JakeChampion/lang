package checker

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNestedFieldTransfers(t *testing.T) {
	data, err := os.ReadFile("testdata/nested_field_transfer_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Source, Want string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := checkSource(t, tc.Source)
			if tc.Want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			for _, code := range strings.Split(tc.Want, "|") {
				if hasCode(err, code) {
					return
				}
			}
			t.Fatalf("want %s, got %v", tc.Want, err)
		})
	}
}
