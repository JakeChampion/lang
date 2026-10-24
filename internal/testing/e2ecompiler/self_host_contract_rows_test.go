package e2ecompiler

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostContractRowsFoldEveryCalleeFact(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("../../../compiler/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	semlower := read("semlower.fern")
	fn := regexp.MustCompile(`(?s)\nfunction contract_rows\(.*?\n\}\n`).FindString(semlower)
	if fn == "" {
		t.Fatal("cannot find contract_rows in semlower.fern")
	}

	contract := regexp.MustCompile(`(?m)^pub struct Contract \{([^}]*)\}`).FindStringSubmatch(read("ssasem.fern"))
	if contract == nil {
		t.Fatal("cannot find `pub struct Contract` in ssasem.fern")
	}
	fields := regexp.MustCompile(`([a-z_0-9]+):`).FindAllStringSubmatch(contract[1], -1)
	if len(fields) < 4 {
		t.Fatalf("parsed only %d Contract fields; the pattern no longer matches the struct", len(fields))
	}
	var missing []string
	for _, f := range fields {
		if !strings.Contains(fn, "c."+f[1]) {
			missing = append(missing, "Contract."+f[1])
		}
	}

	call := regexp.MustCompile(`= produced_body\(([^;]*)\);`).FindStringSubmatch(semlower)
	if call == nil {
		t.Fatal("cannot find the produced_body call in semlower.fern")
	}
	tables := regexp.MustCompile(`\bpl\.r\.([a-z_0-9]+)\b`).FindAllStringSubmatch(call[1], -1)
	if len(tables) < 3 {
		t.Fatalf("produced_body passes only %d Rows tables; the pattern no longer matches the call", len(tables))
	}
	for _, f := range tables {
		if f[1] == "plans" {
			continue
		}
		if !strings.Contains(fn, "r."+f[1]+".") {
			missing = append(missing, "Rows."+f[1])
		}
	}
	if len(missing) > 0 {
		t.Fatalf("contract_rows does not fold: %s", strings.Join(missing, ", "))
	}
}
