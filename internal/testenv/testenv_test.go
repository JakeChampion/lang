package testenv

import (
	"os"
	"sort"
	"strings"
	"testing"
)

func value(env []string, name string) (string, bool) {
	var got string
	var found bool
	for _, e := range env {
		n, v, ok := strings.Cut(e, "=")
		if ok && n == name {
			if found {
				return "", false // a duplicate binding is never correct
			}
			got, found = v, true
		}
	}
	return got, found
}

// The load-bearing property: no census variable reaches a child by inheritance.
func TestCleanStripsEveryCensusVariable(t *testing.T) {
	for _, v := range Vars {
		if v.Name == AmbientOKVar {
			continue // its own forwarding is covered below
		}
		t.Run(v.Name, func(t *testing.T) {
			t.Setenv(v.Name, "ambient")
			if got, found := value(Clean(), v.Name); found {
				t.Errorf("Clean() forwarded %s=%q from the ambient environment", v.Name, got)
			}
		})
	}
}

func TestCleanKeepsThePassthroughAllowlist(t *testing.T) {
	t.Setenv("PATH", "/sentinel/bin")
	t.Setenv("NIX_CFLAGS_COMPILE", "-isystem /sentinel")
	env := Clean()
	if got, found := value(env, "PATH"); !found || got != "/sentinel/bin" {
		t.Errorf("PATH = %q, %v; want the ambient value", got, found)
	}
	if _, found := value(env, "NIX_CFLAGS_COMPILE"); !found {
		t.Error("NIX_CFLAGS_COMPILE dropped; the nix cc wrapper needs it to link")
	}
}

func TestCleanDropsAnUnclassifiedVariable(t *testing.T) {
	t.Setenv("FERN_NOT_IN_THE_CENSUS", "1")
	if _, found := value(Clean(), "FERN_NOT_IN_THE_CENSUS"); found {
		t.Error("an unclassified variable reached the child; the allowlist is not an allowlist")
	}
}

// A name the test sets must bind exactly once, whatever the ambient value was:
// relying on a duplicate-key resolution rule is how "set it explicitly" quietly
// becomes "inherit it".
func TestWithReplacesRatherThanShadows(t *testing.T) {
	t.Setenv("FERN_TEST_AMBIENT_OK", "FERN_STRICT_IR")
	t.Setenv("FERN_STRICT_IR", "1")
	env := With("FERN_STRICT_IR=0")
	got, found := value(env, "FERN_STRICT_IR")
	if !found || got != "0" {
		t.Fatalf("FERN_STRICT_IR = %q, %v; want a single binding of 0", got, found)
	}
}

func TestWithPassthroughNameAlsoReplaces(t *testing.T) {
	t.Setenv("PATH", "/ambient/bin")
	if got, found := value(With("PATH=/set/bin"), "PATH"); !found || got != "/set/bin" {
		t.Errorf("PATH = %q, %v; want a single binding of /set/bin", got, found)
	}
}

func TestWithPanicsOnAMalformedEntry(t *testing.T) {
	for _, bad := range []string{"FERN_STRICT_IR", "=1"} {
		t.Run(bad, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("With(%q) did not panic; a typo'd knob is silently inert", bad)
				}
			}()
			With(bad)
		})
	}
}

// Acceptance is tested against an explicit environ rather than the process's
// own: a test asserting "this environment is rejected" must not depend on the
// environment the suite was started in.
func TestAmbientOKForwardsAndAccepts(t *testing.T) {
	if err := checkAmbient([]string{"FERN_LEAKCHECK=1"}); err == nil {
		t.Fatal("checkAmbient accepted an unacknowledged FERN_LEAKCHECK=1")
	}
	acked := []string{"FERN_LEAKCHECK=1", AmbientOKVar + "=FERN_LEAKCHECK"}
	if err := checkAmbient(acked); err != nil {
		t.Errorf("checkAmbient rejected an acknowledged variable: %v", err)
	}
	if got, found := value(cleanFrom(acked), "FERN_LEAKCHECK"); !found || got != "1" {
		t.Errorf("acknowledged FERN_LEAKCHECK = %q, %v; want it forwarded so a probe still works", got, found)
	}
	// The acknowledgement itself is not a licence for anything else.
	if err := checkAmbient(append(acked, "FERN_SANITIZE=1")); err == nil {
		t.Error("checkAmbient accepted FERN_SANITIZE because a different variable was acknowledged")
	}
}

func TestAmbientOKCannotForwardAnUnclassifiedName(t *testing.T) {
	t.Setenv(AmbientOKVar, "FERN_NOT_IN_THE_CENSUS")
	t.Setenv("FERN_NOT_IN_THE_CENSUS", "1")
	if _, found := value(Clean(), "FERN_NOT_IN_THE_CENSUS"); found {
		t.Error("the acknowledgement list forwarded a name nothing has classified")
	}
}

func TestWithoutRemovesAnAcknowledgedVariable(t *testing.T) {
	t.Setenv(AmbientOKVar, "FERN_STRICT_IR")
	t.Setenv("FERN_STRICT_IR", "1")
	if _, found := value(Without("FERN_STRICT_IR"), "FERN_STRICT_IR"); found {
		t.Error("Without kept a variable the test asked to be absent")
	}
}

func TestALaneVariableIsNotAmbientForbidden(t *testing.T) {
	if err := checkAmbient([]string{"FERN_NATIVE_ASM=1"}); err != nil {
		t.Errorf("checkAmbient rejected a lane selector CI sets process-wide: %v", err)
	}
}

// The allowlist and the census must not overlap, or a passthrough entry would
// re-admit exactly what Clean exists to remove.
func TestPassthroughCannotAdmitACensusVariable(t *testing.T) {
	for _, v := range Vars {
		if passes(v.Name) {
			t.Errorf("%s is both censused and on the passthrough allowlist", v.Name)
		}
	}
}

func TestCensusIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	names := make([]string, 0, len(Vars))
	for _, v := range Vars {
		if seen[v.Name] {
			t.Errorf("%s is censused twice", v.Name)
		}
		seen[v.Name] = true
		names = append(names, v.Name)
		if v.Class != Semantic && v.Class != Lane {
			t.Errorf("%s has class %q", v.Name, v.Class)
		}
		if strings.TrimSpace(v.Effect) == "" {
			t.Errorf("%s has no effect line; the census is the enumerable list of what an ambient value can do", v.Name)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Error("Vars is not sorted by name")
	}
}

// CheckAmbient names every offender and the escape hatch, so a developer who
// trips it can act without reading this package.
func TestCheckAmbientMessageIsActionable(t *testing.T) {
	err := checkAmbient([]string{"FERN_SANITIZE=1"})
	if err == nil {
		t.Fatal("checkAmbient accepted FERN_SANITIZE=1")
	}
	for _, want := range []string{"FERN_SANITIZE", AmbientOKVar, Lookup("FERN_SANITIZE").Effect} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message omits %q:\n%s", want, err)
		}
	}
}

// os.Environ can hold an entry without '='; neither half may panic on it.
func TestMalformedAmbientEntryIsIgnored(t *testing.T) {
	environ := append(os.Environ(), "NOEQUALS")
	if err := checkAmbient(environ); err != nil && strings.Contains(err.Error(), "NOEQUALS") {
		t.Errorf("checkAmbient reported a malformed entry: %v", err)
	}
	for _, e := range cleanFrom(environ) {
		if e == "NOEQUALS" {
			t.Error("cleanFrom passed a malformed entry through")
		}
	}
}
