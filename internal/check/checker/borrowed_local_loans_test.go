package checker

import (
	"encoding/json"
	"os"
	"testing"
)

func TestBorrowedLocalLoans(t *testing.T) {
	data, err := os.ReadFile("testdata/borrowed_local_loans_cases.json")
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
			} else if !hasCode(err, tc.Want) {
				t.Fatalf("want %s, got %v", tc.Want, err)
			}
		})
	}
}
