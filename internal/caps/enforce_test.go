package caps_test

import (
	"github.com/jakechampion/lang/internal/caps"
	"strings"
	"testing"
)

// caps.Enforce splits caps.Analyze's rows on the grant table: a governed package
// errors outside its grant and stays silent inside it, an ungoverned
// package warns, and the root package is exempt from both.
func TestEnforce(t *testing.T) {
	rows := []caps.Row{
		{Package: "app", Uses: []caps.Use{{Capability: "net", Chain: []string{"main", "tcp_connect"}}}},
		{Package: "governed", Uses: []caps.Use{
			{Capability: "fs", Chain: []string{"lib__save", "write_file"}},
			{Capability: "net", Chain: []string{"lib__save", "tcp_connect"}},
		}},
		{Package: "ungoverned", Uses: []caps.Use{{Capability: "fs", Chain: []string{"lib__save", "write_file"}}}},
	}
	grants := map[string]caps.Grant{
		"app":      {Root: true},
		"governed": {Governed: true, Caps: []string{"fs"}},
	}
	errs, warns := caps.Enforce(rows, grants)
	if len(errs) != 1 || errs[0].Package != "governed" || errs[0].Capability != "net" {
		t.Fatalf("errs: %+v", errs)
	}
	wantErr := `package "governed" reaches 'net' (tcp_connect) without a capability grant: lib__save → tcp_connect; add "net" to its capabilities in fern.toml or remove the call`
	if got := errs[0].Message(); got != wantErr {
		t.Errorf("error message:\ngot  %q\nwant %q", got, wantErr)
	}
	if len(warns) != 1 || warns[0].Package != "ungoverned" || warns[0].Capability != "fs" {
		t.Fatalf("warns: %+v", warns)
	}
	if got := warns[0].Message(); !strings.Contains(got, `package "ungoverned" reaches 'fs' (write_file)`) || !strings.Contains(got, "no capabilities key") {
		t.Errorf("warning message: %q", got)
	}
}
