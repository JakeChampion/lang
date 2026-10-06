package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The self-host front end (parser.fern) carries its own copy of "which type
// names are reserved", because it has no access to builtinStructDecls /
// builtinEnumDecls. Two copies of one list drift, and the drift is silent in
// the direction that matters: a name added here and not there means the
// self-host quietly ACCEPTS a redeclaration native refuses, which is how the
// struct half came to be missing entirely — E010 existed in the self-host for
// enums only, and `struct HttpRequest {}` compiled.
//
// So the lists are pinned to each other, exactly and in both directions. A
// builtin added to either side fails here until both carry it.
//
// This reads the self-host source rather than running its checker: the
// question is whether the two DECLARATIONS agree, which a behavioural test
// can only sample one name at a time.

var (
	selfHostReservedStructsRE = regexp.MustCompile(
		`(?s)function is_reserved_struct_name\(name: string\): boolean \{.*?let reserved: string\[\] = \[(.*?)\]`)
	selfHostReservedEnumsRE = regexp.MustCompile(
		`(?s)function is_reserved_enum_name\(name: string\): boolean \{(.*?)\n\}`)
	fernStringRE              = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`)
	selfHostReservedTParamsRE = regexp.MustCompile(
		`(?s)function reserved_struct_tparams\(name: string\): string\[\] \{(.*?)\n\}`)
	selfHostReservedEnumTParamsRE = regexp.MustCompile(
		`(?s)function reserved_enum_tparams\(name: string\): string\[\] \{(.*?)\n\}`)
	fernTParamsArmRE = regexp.MustCompile(`if \(name == "(\w+)"\) \{\s*return \[(.*?)\];\s*\}`)
)

func selfHostReservedNamesSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "compiler", "parser.fern"))
	if err != nil {
		t.Fatalf("read self-host parser.fern: %v", err)
	}
	return string(b)
}

// namesIn pulls the quoted names out of one matched self-host construct,
// failing when the pattern no longer matches — an unreadable list is an
// unchecked list, not an empty one.
func namesIn(t *testing.T, src string, re *regexp.Regexp, what string) []string {
	t.Helper()
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("cannot find the self-host %s list — the pattern no longer matches, so this test proves nothing", what)
	}
	var out []string
	for _, q := range fernStringRE.FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	sort.Strings(out)
	return out
}

func TestSelfHostReservedTypeNamesMatchBuiltins(t *testing.T) {
	src := selfHostReservedNamesSource(t)

	t.Run("structs", func(t *testing.T) {
		var want []string
		for _, sd := range builtinStructDecls() {
			want = append(want, sd.Name)
		}
		sort.Strings(want)
		got := namesIn(t, src, selfHostReservedStructsRE, "is_reserved_struct_name")
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("is_reserved_struct_name disagrees with builtinStructDecls()\n self-host: %v\n    native: %v\na name in native but not the self-host is a redeclaration the self-host accepts and native refuses", got, want)
		}
	})

	t.Run("struct-type-params", func(t *testing.T) {
		want := map[string]string{}
		for _, sd := range builtinStructDecls() {
			if len(sd.TypeParams) > 0 {
				want[sd.Name] = strings.Join(sd.TypeParams, ",")
			}
		}
		checkReservedTParams(t, src, selfHostReservedTParamsRE, "reserved_struct_tparams", "builtinStructDecls()", want)
	})

	t.Run("enum-type-params", func(t *testing.T) {
		want := map[string]string{}
		for _, ed := range builtinEnumDecls() {
			if len(ed.TypeParams) > 0 {
				want[ed.Name] = strings.Join(ed.TypeParams, ",")
			}
		}
		checkReservedTParams(t, src, selfHostReservedEnumTParamsRE, "reserved_enum_tparams", "builtinEnumDecls()", want)
	})

	t.Run("enums", func(t *testing.T) {
		var want []string
		for _, ed := range builtinEnumDecls() {
			want = append(want, ed.Name)
		}
		sort.Strings(want)
		got := namesIn(t, src, selfHostReservedEnumsRE, "is_reserved_enum_name")
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("is_reserved_enum_name disagrees with builtinEnumDecls()\n self-host: %v\n    native: %v", got, want)
		}
	})
}

// checkReservedTParams compares the arms of the self-host table `fn` against
// the generic built-ins `native` declares.
func checkReservedTParams(t *testing.T, src string, re *regexp.Regexp, fn, native string, want map[string]string) {
	t.Helper()
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("cannot find the self-host %s arms — the pattern no longer matches, so this test proves nothing", fn)
	}
	got := map[string]string{}
	for _, arm := range fernTParamsArmRE.FindAllStringSubmatch(m[1], -1) {
		var params []string
		for _, q := range fernStringRE.FindAllStringSubmatch(arm[2], -1) {
			params = append(params, q[1])
		}
		got[arm[1]] = strings.Join(params, ",")
	}
	if len(got) != len(want) {
		t.Errorf("%s has %d generic built-ins, %s %d\n self-host: %v\n    native: %v", fn, len(got), native, len(want), got, want)
	}
	for name, params := range want {
		if got[name] != params {
			t.Errorf("%s(%q) = [%s], %s has [%s]", fn, name, got[name], native, params)
		}
	}
}
