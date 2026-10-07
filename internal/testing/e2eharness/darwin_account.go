package e2eharness

import (
	"os/exec"
	"os/user"
	"strings"
	"testing"
)

// DarwinAccountAnswers is what the account-entry gates' program must print
// on Darwin, from Go's os/user and the system logname: this user's name, none
// for a uid no account has, root's home, gid 0's name, staff's gid, and the
// session's login name (none when there is none).
func DarwinAccountAnswers(t *testing.T) string {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	root, err := user.Lookup("root")
	if err != nil {
		t.Fatalf("user.Lookup(root): %v", err)
	}
	wheel, err := user.LookupGroupId("0")
	if err != nil {
		t.Fatalf("user.LookupGroupId(0): %v", err)
	}
	staff, err := user.LookupGroup("staff")
	if err != nil {
		t.Fatalf("user.LookupGroup(staff): %v", err)
	}
	login := "none"
	if out, err := exec.Command("/usr/bin/logname").Output(); err == nil {
		login = strings.TrimSpace(string(out))
	}
	return strings.Join([]string{me.Username, "none", root.HomeDir, wheel.Name, staff.Gid, login}, "\n") + "\n"
}
