package main

import (
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

// The committed module must be what the generator produces from the
// committed certdata.txt as of the date it records: generated code, where
// a hand edit or a stale regeneration would otherwise go unseen.
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
	got, _, err := generate(certdata(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s is out of date for cmd/rootsgen/certdata.txt. Re-run: go run ./cmd/rootsgen", path)
	}
}

// Every certificate has a trust object, the trust bits are read as NSS
// reads them, and a distrust date is honoured: left out once it is spent,
// refused while it is live.
func TestTrustAndDistrust(t *testing.T) {
	objects, err := parseCertdata(certdata(t))
	if err != nil {
		t.Fatal(err)
	}
	all, err := roots(objects)
	if err != nil {
		t.Fatal(err)
	}
	var trusted, dated int
	for _, r := range all {
		if r.trusted {
			trusted++
			if !r.distrust.IsZero() {
				dated++
			}
		}
	}
	if trusted < 100 || trusted == len(all) {
		t.Errorf("%d of %d roots trusted for server authentication", trusted, len(all))
	}
	if dated == 0 {
		t.Error("no root carries a distrust date; the attribute was not read")
	}
	src, st, err := generate(certdata(t), time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if st.taken+st.leftOut != trusted || st.leftOut != dated {
		t.Errorf("taken %d + left out %d, want %d trusted with %d dated", st.taken, st.leftOut, trusted, dated)
	}
	if !strings.Contains(src, "//   ISRG Root X1\n") {
		t.Error("ISRG Root X1 is not listed")
	}
	if strings.Contains(src, "//   Entrust Root Certification Authority - G2\n") {
		t.Error("a distrusted root is listed as taken")
	}
	if strings.Count(src, "-----BEGIN CERTIFICATE-----") != st.taken {
		t.Error("the PEM bundle does not hold one block per root taken")
	}
	_, _, err = generate(certdata(t), time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "distrusted for server certificates issued after") {
		t.Errorf("a live distrust date was not refused: %v", err)
	}
}

// Deterministic output is what makes TestCommittedFileIsUpToDate usable.
func TestGenerateIsDeterministic(t *testing.T) {
	asOf := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	a, _, err := generate(certdata(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := generate(certdata(t), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("two runs produced different output")
	}
}
