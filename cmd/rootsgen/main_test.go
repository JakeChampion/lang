package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

func certdata(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("certdata.txt")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func nssVersion(t *testing.T) string {
	t.Helper()
	hdr, err := os.ReadFile("nssckbi.h")
	if err != nil {
		t.Fatal(err)
	}
	v, err := builtinsVersion(hdr)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBuiltinsVersion(t *testing.T) {
	if v := nssVersion(t); !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(v) {
		t.Fatalf("nssckbi.h names builtins version %q", v)
	}
	if _, err := builtinsVersion([]byte("#define NSS_BUILTINS_LIBRARY_MAJOR_VERSION 2\n")); err == nil {
		t.Fatal("a header without the version define was accepted")
	}
}

func TestCommittedFileIsUpToDate(t *testing.T) {
	const path = "../../" + outputPath
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`as of (\d{4}-\d{2}-\d{2})\.`).FindSubmatch(want)
	if m == nil {
		t.Fatalf("%s does not say when it was generated", path)
	}
	asOf, err := time.Parse("2006-01-02", string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := generate(certdata(t), nssVersion(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s is out of date for cmd/rootsgen/certdata.txt. Re-run: go run ./cmd/rootsgen", path)
	}
}

func TestTrustAndDistrust(t *testing.T) {
	objects, err := parseCertdata(certdata(t))
	if err != nil {
		t.Fatal(err)
	}
	all, err := roots(objects)
	if err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	var trusted, live, spent int
	for _, r := range all {
		if !r.trusted {
			continue
		}
		trusted++
		switch {
		case r.distrust.IsZero():
		case asOf.Before(r.distrust.Add(leafLifetime)):
			live++
		default:
			spent++
		}
	}
	if trusted < 100 || trusted == len(all) {
		t.Errorf("%d of %d roots trusted for server authentication", trusted, len(all))
	}
	if live+spent == 0 {
		t.Error("no root carries a distrust date; the attribute was not read")
	}
	src, st, err := generate(certdata(t), nssVersion(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if st.taken+st.leftOut != trusted || st.leftOut != spent || st.dated != live {
		t.Errorf("taken %d (%d dated) + left out %d, want %d trusted with %d live and %d spent dates", st.taken, st.dated, st.leftOut, trusted, live, spent)
	}
	if !strings.Contains(src, "//   ISRG Root X1\n") {
		t.Error("ISRG Root X1 is not listed")
	}
	if strings.Count(src, "-----BEGIN CERTIFICATE-----") != st.taken {
		t.Error("the PEM bundle does not hold one block per root taken")
	}
	if strings.Count(src, " as i64") != st.taken || strings.Count(src, "0 as i64") != st.taken-st.dated {
		t.Error("__distrust does not hold one date per root taken, zero for the undated")
	}
	var dated root
	for _, r := range all {
		if r.trusted && !r.distrust.IsZero() {
			dated = r
			break
		}
	}
	before, bst, err := generate(certdata(t), nssVersion(t), dated.distrust.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("//   %s (%s)\n", dated.label, dated.distrust.Format("2006-01-02"))
	if bst.dated == 0 || !strings.Contains(before, "limits:\n"+line) || !strings.Contains(before, fmt.Sprintf("%d as i64", dated.distrust.Unix())) {
		t.Errorf("%q is not bundled with its live date", dated.label)
	}
	after, ast, err := generate(certdata(t), nssVersion(t), dated.distrust.Add(leafLifetime))
	if err != nil {
		t.Fatal(err)
	}
	if ast.leftOut == 0 || strings.Contains(after, "//   "+dated.label+"\n") || !strings.Contains(after, "from:\n"+line) {
		t.Errorf("%q is not left out once its date is spent", dated.label)
	}
}

func TestUnreadDistrustDateRefused(t *testing.T) {
	const object = `CKA_CLASS CK_OBJECT_CLASS CKO_CERTIFICATE
CKA_LABEL UTF8 "Example Root"
CKA_ISSUER MULTILINE_OCTAL
\060\001
END
CKA_SERIAL_NUMBER MULTILINE_OCTAL
\002\001
END
CKA_VALUE MULTILINE_OCTAL
\060\000
END
CKA_NSS_SERVER_DISTRUST_AFTER %s
CKA_CLASS CK_OBJECT_CLASS CKO_NSS_TRUST
CKA_ISSUER MULTILINE_OCTAL
\060\001
END
CKA_SERIAL_NUMBER MULTILINE_OCTAL
\002\001
END
CKA_TRUST_SERVER_AUTH CK_TRUST CKT_NSS_TRUSTED_DELEGATOR
`
	for _, tc := range []struct {
		attr string
		ok   bool
	}{
		{"CK_BBOOL CK_FALSE", true},
		{"MULTILINE_OCTAL\n\\062\\064\\061\\061\\063\\060\\062\\063\\065\\071\\065\\071\\132\nEND", true},
		{"UTF8 \"241130235959Z\"", false},
		{"CK_BBOOL CK_TRUE", false},
	} {
		objects, err := parseCertdata([]byte(fmt.Sprintf(object, tc.attr)))
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.attr, err)
		}
		_, err = roots(objects)
		if (err == nil) != tc.ok {
			t.Errorf("%s: roots() error %v, want ok=%v", tc.attr, err, tc.ok)
		}
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	asOf := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	a, _, err := generate(certdata(t), nssVersion(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := generate(certdata(t), nssVersion(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("two runs produced different output")
	}
}
