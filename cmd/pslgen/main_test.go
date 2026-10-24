package main

import (
	"os"
	"strings"
	"testing"
)

func list(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("public_suffix_list.dat")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCommittedFileIsUpToDate(t *testing.T) {
	const path = "../../" + outputPath
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := generate(list(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s is out of date for cmd/pslgen/public_suffix_list.dat. Re-run: go run ./cmd/pslgen", path)
	}
}

func TestPunycode(t *testing.T) {
	cases := map[string]string{
		"bücher":            "xn--bcher-kva",
		"münchen":           "xn--mnchen-3ya",
		"公司":                "xn--55qx5d",
		"中国":                "xn--fiqs8s",
		"食狮":                "xn--85x722f",
		"aéroport":          "xn--aroport-bya",
		"ليهمابتكلموشعربي؟": "xn--egbpdaj6bu4bxfgehfvwxn",
		"他们为什么不说中文":         "xn--ihqwcrb4cv8a8dqg056pqjye",
		"-> $1.00 <-":       "xn---> $1.00 <--",
	}
	for in, want := range cases {
		got, err := punycode(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestRules(t *testing.T) {
	rules, err := icannRules(list(t))
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, r := range rules {
		has[r] = true
		if !isASCII(r) {
			t.Errorf("%q is not ASCII", r)
		}
	}
	for _, want := range []string{"com", "co.uk", "*.ck", "!www.ck", "xn--55qx5d.cn", "xn--fiqs8s", "k12.ak.us"} {
		if !has[want] {
			t.Errorf("rule %q missing", want)
		}
	}
	for _, private := range []string{"github.io", "blogspot.com", "uk.com"} {
		if has[private] {
			t.Errorf("private rule %q taken", private)
		}
	}
	if len(rules) < 6000 {
		t.Errorf("only %d rules", len(rules))
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	a, _, err := generate(list(t))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := generate(list(t))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("two runs differ")
	}
	if !strings.Contains(a, `\nco.uk\n`) {
		t.Fatal("co.uk is not on its own line")
	}
}
