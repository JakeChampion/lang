package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// fern -apk with an OUT that ends in .aab writes an Android App Bundle: the
// module base with the manifest and the resources as protocol buffers, the
// libraries and the classes, and a JAR signature that verifies with the
// certificate of the key. bundletool checks the bundle on CI, in the
// android-stopwatch-apk lane.
func TestApkBundle(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "tune.aab")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out, e2eharness.RepoPath("examples/android/tune", "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	if _, err := os.Stat(filepath.Join(dir, "tune.key.pem")); err != nil {
		t.Errorf("no key next to the bundle: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr := zipOf(t, raw)
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := "META-INF/MANIFEST.MF META-INF/FERN.SF META-INF/FERN.EC BundleConfig.pb base/manifest/AndroidManifest.xml base/dex/classes.dex base/resources.pb base/res/drawable/icon.png base/res/layout/widget.xml base/res/xml/widget.xml base/res/xml/data_extraction_rules.xml base/res/xml/full_backup_content.xml base/lib/arm64-v8a/libapp.so base/lib/x86_64/libapp.so"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("entries = %s\nwant %s", got, want)
	}

	man := readZipEntry(t, zr, "base/manifest/AndroidManifest.xml")
	if !wellFormedProto(man) {
		t.Error("the manifest is not a well-formed protocol buffer")
	}
	for _, s := range []string{"dev.fern.tune", "targetSdkVersion", "enableOnBackInvokedCallback", "android.app.lib_name"} {
		if !bytes.Contains(man, []byte(s)) {
			t.Errorf("the manifest has no %q", s)
		}
	}
	res := readZipEntry(t, zr, "base/resources.pb")
	if !wellFormedProto(res) || !bytes.Contains(res, []byte("res/drawable/icon.png")) {
		t.Error("resources.pb is not a resource table with the icon")
	}

	mf := readZipEntry(t, zr, "META-INF/MANIFEST.MF")
	sf := readZipEntry(t, zr, "META-INF/FERN.SF")
	for _, name := range names[3:] {
		sum := sha256.Sum256(readZipEntry(t, zr, name))
		section := "Name: " + name + "\r\nSHA-256-Digest: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"
		if !bytes.Contains(mf, []byte(section)) {
			t.Errorf("MANIFEST.MF has no digest of %s", name)
		}
		sectionSum := sha256.Sum256([]byte(section))
		if !bytes.Contains(sf, []byte("Name: "+name+"\r\nSHA-256-Digest: "+base64.StdEncoding.EncodeToString(sectionSum[:])+"\r\n")) {
			t.Errorf("FERN.SF has no digest of the section of %s", name)
		}
	}
	mfSum := sha256.Sum256(mf)
	if !bytes.Contains(sf, []byte("SHA-256-Digest-Manifest: "+base64.StdEncoding.EncodeToString(mfSum[:])+"\r\n")) {
		t.Error("FERN.SF has no digest of MANIFEST.MF")
	}
	cert, sig := pkcs7Signer(t, readZipEntry(t, zr, "META-INF/FERN.EC"))
	key, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("the certificate has a %T key, want ECDSA", cert.PublicKey)
	}
	sfSum := sha256.Sum256(sf)
	if !ecdsa.VerifyASN1(key, sfSum[:], sig) {
		t.Error("the signature of FERN.SF does not verify with the certificate")
	}
}

// pkcs7Signer is the certificate and the signature of the one signer of a
// PKCS #7 SignedData.
func pkcs7Signer(t *testing.T, der []byte) (*x509.Certificate, []byte) {
	t.Helper()
	var info struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if _, err := asn1.Unmarshal(der, &info); err != nil {
		t.Fatalf("FERN.EC: %v", err)
	}
	if !info.Type.Equal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}) {
		t.Fatalf("FERN.EC holds %v, want signedData", info.Type)
	}
	var signed struct {
		Version      int
		Digests      asn1.RawValue
		Content      asn1.RawValue
		Certificates asn1.RawValue `asn1:"tag:0"`
		Signers      asn1.RawValue
	}
	if _, err := asn1.Unmarshal(info.Content.Bytes, &signed); err != nil {
		t.Fatalf("SignedData: %v", err)
	}
	cert, err := x509.ParseCertificate(signed.Certificates.Bytes)
	if err != nil {
		t.Fatalf("the certificate: %v", err)
	}
	var signer struct {
		Version   int
		Issuer    asn1.RawValue
		Digest    asn1.RawValue
		Algorithm asn1.RawValue
		Signature []byte
	}
	if _, err := asn1.Unmarshal(signed.Signers.Bytes, &signer); err != nil {
		t.Fatalf("SignerInfo: %v", err)
	}
	return cert, signer.Signature
}

// wellFormedProto reports whether b is a sequence of protocol buffer fields
// of the wire types that aapt2 writes: varint, length-delimited and 32-bit.
func wellFormedProto(b []byte) bool {
	varint := func() (uint64, bool) {
		var v uint64
		for shift := 0; shift < 64; shift += 7 {
			if len(b) == 0 {
				return 0, false
			}
			c := b[0]
			b = b[1:]
			v |= uint64(c&127) << shift
			if c < 128 {
				return v, true
			}
		}
		return 0, false
	}
	for len(b) > 0 {
		tag, ok := varint()
		if !ok || tag>>3 == 0 {
			return false
		}
		switch tag & 7 {
		case 0:
			if _, ok := varint(); !ok {
				return false
			}
		case 2:
			n, ok := varint()
			if !ok || n > uint64(len(b)) {
				return false
			}
			b = b[n:]
		case 5:
			if len(b) < 4 {
				return false
			}
			b = b[4:]
		default:
			return false
		}
	}
	return true
}
