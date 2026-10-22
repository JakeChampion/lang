// Command rootsgen generates internal/stdlib/std/tls/mozilla.fern — the
// roots Mozilla trusts for server authentication — from the NSS
// certdata.txt committed beside it.
//
// Usage, from the repository root:
//
//	go run ./cmd/rootsgen
//
// To refresh the list: replace cmd/rootsgen/certdata.txt and nssckbi.h
// with the current lib/ckfw/builtins/certdata.txt and nssckbi.h from
// NSS's own repository, not mozilla-central, which takes an NSS change
// only when Firefox next imports NSS (the builtins version is read from
// the header's NSS_BUILTINS_LIBRARY_VERSION) and re-run; that is what
// `scripts/refresh-data roots` does, and what the refresh-data workflow
// does every week. TestCommittedFileIsUpToDate fails until the output is
// committed.
//
// A root is taken when its trust object marks it CKT_NSS_TRUSTED_DELEGATOR
// for server authentication. One Mozilla distrusts for server certificates
// issued after a date (CKA_NSS_SERVER_DISTRUST_AFTER) is bundled with that
// date, which std/tls/verify enforces against the leaf's notBefore, while
// a certificate issued before the date can still be valid, and left out
// once none can.
//
// The list is one string literal, not an array of them: a Fern array
// literal is code that builds the array on every call, a string literal
// is static data (cmd/unicodegen says more).
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	certdataPath = "cmd/rootsgen/certdata.txt"
	headerPath   = "cmd/rootsgen/nssckbi.h"
	outputPath   = "internal/stdlib/std/tls/mozilla.fern"
	// leafLifetime is the longest validity a server certificate issued
	// before any distrust date in the file could carry (CA/Browser Forum
	// Baseline Requirements §6.3.2: 398 days since 2020-09-01).
	leafLifetime = 398 * 24 * time.Hour
)

func main() {
	data, err := os.ReadFile(certdataPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hdr, err := os.ReadFile(headerPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	nssVersion, err := builtinsVersion(hdr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rootsgen:", err)
		os.Exit(1)
	}
	out, stats, err := generate(data, nssVersion, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, "rootsgen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputPath, []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d roots, %d left out, NSS %s)\n", outputPath, stats.taken, stats.leftOut, nssVersion)
}

// builtinsVersion is NSS_BUILTINS_LIBRARY_VERSION as nssckbi.h defines it.
func builtinsVersion(header []byte) (string, error) {
	m := regexp.MustCompile(`(?m)^#define NSS_BUILTINS_LIBRARY_VERSION "([0-9.]+)"`).FindSubmatch(header)
	if m == nil {
		return "", fmt.Errorf("%s does not define NSS_BUILTINS_LIBRARY_VERSION", headerPath)
	}
	return string(m[1]), nil
}

// attr is one attribute of a certdata.txt object: its PKCS #11 type and
// its value, as text for a token or a quoted string and as bytes for a
// MULTILINE_OCTAL block.
type attr struct {
	typ   string
	text  string
	bytes []byte
}

// parseCertdata reads certdata.txt's objects, each a run of attribute
// lines opened by CKA_CLASS.
func parseCertdata(data []byte) ([]map[string]attr, error) {
	var objects []map[string]attr
	var cur map[string]attr
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "" || strings.HasPrefix(line, "#") || line == "BEGINDATA" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("line %d: %q is not an attribute", i+1, line)
		}
		name, typ := fields[0], fields[1]
		a := attr{typ: typ}
		switch typ {
		case "MULTILINE_OCTAL":
			var b []byte
			for i++; i < len(lines) && strings.TrimRight(lines[i], "\r") != "END"; i++ {
				v, err := octal(strings.TrimRight(lines[i], "\r"))
				if err != nil {
					return nil, fmt.Errorf("line %d: %v", i+1, err)
				}
				b = append(b, v...)
			}
			if i >= len(lines) {
				return nil, fmt.Errorf("%s: MULTILINE_OCTAL without END", name)
			}
			a.bytes = b
		case "UTF8":
			rest := strings.TrimSpace(strings.TrimPrefix(line, name+" UTF8"))
			if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
				return nil, fmt.Errorf("line %d: %s is not a quoted string", i+1, name)
			}
			a.text = rest[1 : len(rest)-1]
		default:
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: %q has %d fields", i+1, line, len(fields))
			}
			a.text = fields[2]
		}
		if name == "CKA_CLASS" {
			cur = map[string]attr{}
			objects = append(objects, cur)
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: %s before any CKA_CLASS", i+1, name)
		}
		cur[name] = a
	}
	return objects, nil
}

// octal decodes one MULTILINE_OCTAL line: a run of \ooo escapes.
func octal(line string) ([]byte, error) {
	var out []byte
	for line != "" {
		if len(line) < 4 || line[0] != '\\' {
			return nil, fmt.Errorf("%q is not a run of \\ooo escapes", line)
		}
		v, err := strconv.ParseUint(line[1:4], 8, 8)
		if err != nil {
			return nil, fmt.Errorf("%q: %v", line[:4], err)
		}
		out = append(out, byte(v))
		line = line[4:]
	}
	return out, nil
}

// root is one certificate object of certdata.txt with what its trust
// object says about server authentication. A distrust date is read in
// the two encodings the file uses, a MULTILINE_OCTAL time and CK_FALSE
// for none; any other fails, so a distrusted root cannot slip in
// unread.
type root struct {
	label    string
	der      []byte
	trusted  bool
	distrust time.Time // zero when Mozilla sets no date
}

// roots pairs every certificate object with its trust object by issuer
// and serial, in the file's order.
func roots(objects []map[string]attr) ([]root, error) {
	key := func(o map[string]attr) string {
		return string(o["CKA_ISSUER"].bytes) + "\x00" + string(o["CKA_SERIAL_NUMBER"].bytes)
	}
	trust := map[string]map[string]attr{}
	for _, o := range objects {
		if o["CKA_CLASS"].text == "CKO_NSS_TRUST" {
			trust[key(o)] = o
		}
	}
	var out []root
	for _, o := range objects {
		if o["CKA_CLASS"].text != "CKO_CERTIFICATE" {
			continue
		}
		r := root{label: o["CKA_LABEL"].text, der: o["CKA_VALUE"].bytes}
		t, ok := trust[key(o)]
		if !ok {
			return nil, fmt.Errorf("%q has no trust object", r.label)
		}
		r.trusted = t["CKA_TRUST_SERVER_AUTH"].text == "CKT_NSS_TRUSTED_DELEGATOR"
		if d, ok := o["CKA_NSS_SERVER_DISTRUST_AFTER"]; ok {
			switch {
			case d.typ == "MULTILINE_OCTAL":
				when, err := time.Parse("060102150405Z", string(d.bytes))
				if err != nil {
					return nil, fmt.Errorf("%q: distrust date %q: %v", r.label, d.bytes, err)
				}
				r.distrust = when
			case d.typ == "CK_BBOOL" && d.text == "CK_FALSE":
			default:
				return nil, fmt.Errorf("%q: distrust date in an encoding this generator does not read: %s %s", r.label, d.typ, d.text)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

type stats struct {
	taken, dated, leftOut int
}

// generate is the module's source for certdata.txt as of a date: the date
// decides which distrusted roots can still have a valid certificate, and
// so are bundled with their date rather than left out.
func generate(data []byte, nssVersion string, asOf time.Time) (string, stats, error) {
	objects, err := parseCertdata(data)
	if err != nil {
		return "", stats{}, err
	}
	all, err := roots(objects)
	if err != nil {
		return "", stats{}, err
	}
	var taken []root
	var leftOut []root
	for _, r := range all {
		if !r.trusted {
			continue
		}
		if _, err := x509.ParseCertificate(r.der); err != nil {
			return "", stats{}, fmt.Errorf("%q: %v", r.label, err)
		}
		if !r.distrust.IsZero() && !asOf.Before(r.distrust.Add(leafLifetime)) {
			leftOut = append(leftOut, r)
			continue
		}
		taken = append(taken, r)
	}
	var dated []root
	for _, r := range taken {
		if !r.distrust.IsZero() {
			dated = append(dated, r)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, header, nssVersion, sha256.Sum256(data), asOf.Format("2006-01-02"))
	b.WriteString("// The roots, in certdata.txt's order:\n")
	for _, r := range taken {
		fmt.Fprintf(&b, "//   %s\n", r.label)
	}
	b.WriteString("//\n// Distrusted for server certificates issued after the date, which the\n// list carries and std/tls/verify enforces:\n")
	if len(dated) == 0 {
		b.WriteString("//   (none)\n")
	}
	for _, r := range dated {
		fmt.Fprintf(&b, "//   %s (%s)\n", r.label, r.distrust.Format("2006-01-02"))
	}
	b.WriteString("//\n// Left out, distrusted for server certificates issued after a date no\n// certificate issued before can still be valid from:\n")
	if len(leftOut) == 0 {
		b.WriteString("//   (none)\n")
	}
	for _, r := range leftOut {
		fmt.Fprintf(&b, "//   %s (%s)\n", r.label, r.distrust.Format("2006-01-02"))
	}
	fmt.Fprintf(&b, body, nssVersion, len(taken))
	var text bytes.Buffer
	for _, r := range taken {
		if err := pem.Encode(&text, &pem.Block{Type: "CERTIFICATE", Bytes: r.der}); err != nil {
			return "", stats{}, err
		}
	}
	b.WriteString(strings.ReplaceAll(text.String(), "\n", `\n`))
	b.WriteString("\";\n}\n")
	b.WriteString("\n// __distrust is, per root, the Unix second after which a server\n// certificate issued under it is refused; 0 for none.\nfunction __distrust(): i64[] {\n  return [")
	for i, r := range taken {
		if i > 0 {
			b.WriteString(", ")
		}
		if r.distrust.IsZero() {
			b.WriteString("0 as i64")
		} else {
			fmt.Fprintf(&b, "%d as i64", r.distrust.Unix())
		}
	}
	b.WriteString("];\n}\n")
	return b.String(), stats{taken: len(taken), dated: len(dated), leftOut: len(leftOut)}, nil
}

const header = `// std/tls/mozilla — the roots Mozilla trusts for server authentication,
// bundled for a program whose machine has no bundle to read: a WASI
// component, a container image without ca-certificates. A program that
// imports it carries the list; one that does not pays nothing for it.
//
// GENERATED by cmd/rootsgen from NSS's certdata.txt (builtins %s, sha256
// %x), as of %s.
// Do NOT edit by hand; re-run ` + "`go run ./cmd/rootsgen`" + ` after refreshing
// cmd/rootsgen/certdata.txt and nssckbi.h (` + "`scripts/refresh-data roots`" + `).
//
// The list goes stale: it is Mozilla's on the day it was taken, so a root
// added since is missing and one withdrawn since is still trusted until the
// file is regenerated. Prefer the machine's bundle where there is one
// (` + "`verify.system_roots()`" + `), which its distribution keeps current.
//
`

const body = `
import "std/tls/verify";

// NSS_VERSION is the NSS builtins release the list was taken from.
pub const NSS_VERSION: string = "%s";

// COUNT is how many roots the list holds.
pub const COUNT: i32 = %d;

// roots is the list as ` + "`verify.Roots`" + `, every certificate of it parsed,
// each distrusted root with its date.
pub function roots(): verify.Roots {
  match (verify.roots_from_pem(__pem())) {
    Ok(r) => {
      return verify.Roots { ...r, distrust_after: __distrust() };
    },
    Err(_) => {
      return verify.roots([]);
    }
  }
  return verify.roots([]);
}

// __pem is the list as one PEM bundle.
function __pem(): string {
  return "`
