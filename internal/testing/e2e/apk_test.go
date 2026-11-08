package e2e

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"hash/adler32"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func buildApk(t *testing.T, dir string, extra ...string) (string, []byte) {
	t.Helper()
	out := filepath.Join(dir, "app.apk")
	args := append([]string{"-apk", out}, extra...)
	args = append(args, e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, raw
}

// fern -apk writes a zip of stored, aligned entries with the manifest and a
// library for each ABI, and a v2 signature that verifies.
func TestApkWriter(t *testing.T) {
	dir := t.TempDir()
	path, raw := buildApk(t, dir, "-apk-label", "Fern Stopwatch", "-apk-package", "dev.fern.test", "-apk-min-sdk", "28", "-apk-permission", "RECORD_AUDIO,android.permission.INTERNET")
	if info, err := os.Stat(filepath.Join(dir, "app.key.pem")); err != nil {
		t.Fatalf("no new key next to the APK: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("the signing key has permissions %04o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".lib"); err == nil {
		t.Errorf("the build directory %s.lib is left behind", path)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Method != zip.Store {
			t.Errorf("%s: method %d, want stored", f.Name, f.Method)
		}
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		align := int64(4)
		if strings.HasSuffix(f.Name, ".so") {
			align = 16384
		}
		if off%align != 0 {
			t.Errorf("%s: data at %d, want a multiple of %d", f.Name, off, align)
		}
	}
	if got := strings.Join(names, " "); got != "AndroidManifest.xml lib/arm64-v8a/libapp.so lib/x86_64/libapp.so" {
		t.Errorf("entries = %s", got)
	}
	man := readZipEntry(t, zr, "AndroidManifest.xml")
	for _, s := range []string{"dev.fern.test", "Fern Stopwatch", "android.app.NativeActivity", "android.app.lib_name", "app", "uses-permission", "android.permission.RECORD_AUDIO", "android.permission.INTERNET"} {
		if !bytes.Contains(man, utf16le(s)) {
			t.Errorf("the manifest has no string %q", s)
		}
	}
	if v, ok := applicationAttr(t, man, extractNativeLibsID); !ok || v != 0 {
		t.Errorf("android:extractNativeLibs = %#x (set %v), want false for stored libraries", v, ok)
	}
	verifyApkV2(t, raw)
	if apksigner, err := exec.LookPath("apksigner"); err == nil {
		if o, err := exec.Command(apksigner, "verify", "--min-sdk-version", "24", path).CombinedOutput(); err != nil {
			t.Errorf("apksigner verify: %v\n%s", err, o)
		}
	}
	if aapt, err := exec.LookPath("aapt"); err == nil {
		o, err := exec.Command(aapt, "dump", "badging", path).CombinedOutput()
		if err != nil || !strings.Contains(string(o), "package: name='dev.fern.test'") || !strings.Contains(string(o), "native-code: 'arm64-v8a' 'x86_64'") ||
			!strings.Contains(string(o), "sdkVersion:'28'") || !strings.Contains(string(o), "uses-permission: name='android.permission.RECORD_AUDIO'") {
			t.Errorf("aapt dump badging: %v\n%s", err, o)
		}
	}

	// The same key gives the same APK.
	_, again := buildApk(t, dir, "-apk-label", "Fern Stopwatch", "-apk-package", "dev.fern.test", "-apk-min-sdk", "28", "-apk-permission", "RECORD_AUDIO,android.permission.INTERNET")
	if !bytes.Equal(raw, again) {
		t.Error("two builds with the same key give different APKs")
	}
}

// -apk-compress deflates the libraries, and the manifest tells Android to
// extract them. The other entries stay stored, and the signature verifies.
func TestApkCompress(t *testing.T) {
	dir := t.TempDir()
	_, stored := buildApk(t, dir)
	path, raw := buildApk(t, dir, "-apk-compress")
	if len(raw) >= len(stored) {
		t.Errorf("the compressed APK has %d bytes, and the stored one %d", len(raw), len(stored))
	}
	zs, err := zip.NewReader(bytes.NewReader(stored), int64(len(stored)))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		want := zip.Store
		if strings.HasSuffix(f.Name, ".so") {
			want = zip.Deflate
			if !bytes.Equal(readZipEntry(t, zr, f.Name), readZipEntry(t, zs, f.Name)) {
				t.Errorf("%s inflates to other bytes than the stored library", f.Name)
			}
		}
		if f.Method != want {
			t.Errorf("%s: method %d, want %d", f.Name, f.Method, want)
		}
		if off, err := f.DataOffset(); err != nil || off%4 != 0 {
			t.Errorf("%s: data at %d, want a multiple of 4 (%v)", f.Name, off, err)
		}
	}
	if v, ok := applicationAttr(t, readZipEntry(t, zr, "AndroidManifest.xml"), extractNativeLibsID); !ok || v != 0xffffffff {
		t.Errorf("android:extractNativeLibs = %#x (set %v), want true for deflated libraries", v, ok)
	}
	verifyApkV2(t, raw)
	if apksigner, err := exec.LookPath("apksigner"); err == nil {
		if o, err := exec.Command(apksigner, "verify", "--min-sdk-version", "24", path).CombinedOutput(); err != nil {
			t.Errorf("apksigner verify: %v\n%s", err, o)
		}
	}
}

const extractNativeLibsID = 0x010104ea

const usesCleartextTrafficID = 0x010104ec

// TestApkCleartext checks that -apk-cleartext sets
// android:usesCleartextTraffic, so the Java networking of the app, such as
// a media player, can use http://, and that an APK without it leaves the
// attribute out.
func TestApkCleartext(t *testing.T) {
	dir := t.TempDir()
	_, plain := buildApk(t, dir)
	if v, ok := applicationAttr(t, readZipEntry(t, zipOf(t, plain), "AndroidManifest.xml"), usesCleartextTrafficID); ok {
		t.Errorf("android:usesCleartextTraffic = %#x with no -apk-cleartext, want no attribute", v)
	}
	_, raw := buildApk(t, dir, "-apk-cleartext")
	if v, ok := applicationAttr(t, readZipEntry(t, zipOf(t, raw), "AndroidManifest.xml"), usesCleartextTrafficID); !ok || v != 0xffffffff {
		t.Errorf("android:usesCleartextTraffic = %#x (set %v), want true", v, ok)
	}
	if v, ok := applicationAttr(t, readZipEntry(t, zipOf(t, raw), "AndroidManifest.xml"), extractNativeLibsID); !ok || v != 0 {
		t.Errorf("android:extractNativeLibs = %#x (set %v), want false beside the cleartext attribute", v, ok)
	}
	verifyApkV2(t, raw)
}

func zipOf(t *testing.T, raw []byte) *zip.Reader {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return z
}

// The value of the attribute with the resource ID `id` on the application
// element of the binary manifest `man`. The application element is the one
// with android:hasCode.
func applicationAttr(t *testing.T, man []byte, id uint32) (uint32, bool) {
	t.Helper()
	le := binary.LittleEndian
	var resmap []uint32
	for off := 8; off+8 <= len(man); {
		typ := le.Uint16(man[off:])
		size := int(le.Uint32(man[off+4:]))
		if size < 8 || off+size > len(man) {
			t.Fatalf("the manifest has a bad chunk at %d", off)
		}
		switch typ {
		case 0x180:
			for p := off + 8; p+4 <= off+size; p += 4 {
				resmap = append(resmap, le.Uint32(man[p:]))
			}
		case 0x102:
			attrs := map[uint32]uint32{}
			for k := 0; k < int(le.Uint16(man[off+28:])); k++ {
				a := off + 36 + 20*k
				if name := int(le.Uint32(man[a+4:])); name < len(resmap) {
					attrs[resmap[name]] = le.Uint32(man[a+16:])
				}
			}
			if _, ok := attrs[0x0101000c]; ok {
				v, ok := attrs[id]
				return v, ok
			}
		}
		off += size
	}
	t.Fatal("the manifest has no application element")
	return 0, false
}

func TestApkRefusals(t *testing.T) {
	dir := t.TempDir()
	for _, flag := range [][]string{{"-target", "arm64-android"}, {"-shared"}, {"-o", filepath.Join(dir, "x")}} {
		args := append([]string{"-apk", filepath.Join(dir, "x.apk")}, flag...)
		args = append(args, e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t))
		cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
		cmd.Env = e2eharness.ChildEnv()
		o, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(o), "-apk builds the library for both Android ABIs itself, so it takes no "+flag[0]) {
			t.Errorf("%v: want a refusal, got %v\n%s", flag, err, o)
		}
	}
	for _, level := range []string{"9", "35", "x"} {
		args := []string{"-apk", filepath.Join(dir, "z.apk"), "-apk-min-sdk", level, e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t)}
		cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
		cmd.Env = e2eharness.ChildEnv()
		if o, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(o), "-apk-min-sdk takes an API level from 24 to 34") {
			t.Errorf("-apk-min-sdk %s: want a refusal, got %v\n%s", level, err, o)
		}
	}
	notPng := filepath.Join(dir, "icon.txt")
	if err := os.WriteFile(notPng, []byte("not a picture"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, icon := range []string{notPng, filepath.Join(dir, "missing.png")} {
		args := []string{"-apk", filepath.Join(dir, "w.apk"), "-apk-icon", icon, e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t)}
		cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
		cmd.Env = e2eharness.ChildEnv()
		if o, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(o), "-apk-icon") {
			t.Errorf("-apk-icon %s: want a refusal, got %v\n%s", icon, err, o)
		}
	}
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-apk", filepath.Join(dir, "y.apk"), "-apk-key", bad, e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t)}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(o), "is not a P-256 private key in PEM form") {
		t.Errorf("a bad key: want a refusal, got %v\n%s", err, o)
	}
	t.Run("existing-key-link", func(t *testing.T) {
		target := filepath.Join(dir, "missing-key.pem")
		key := filepath.Join(dir, "linked-key.pem")
		if err := os.Symlink(target, key); err != nil {
			t.Fatal(err)
		}
		args := []string{"-apk", filepath.Join(dir, "linked.apk"), "-apk-key", key,
			e2eharness.RepoPath(nativeApp, "app.fern"), e2eharness.SelfHostStdlibRoot(t)}
		cmd := exec.Command(e2eharness.SelfHostCLI(t), args...)
		cmd.Env = e2eharness.ChildEnv()
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "cannot write") {
			t.Errorf("an existing key link: want a refusal, got %v\n%s", err, out)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("the key link target changed: %v", err)
		}
	})
}

// -apk-icon puts the PNG and a resource table that names it in the APK, and
// the application and the activity of the manifest refer to it.
func TestApkIcon(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 48, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 48; x++ {
			img.Set(x, y, color.RGBA{215, 25, 33, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	icon := filepath.Join(dir, "icon.png")
	if err := os.WriteFile(icon, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	path, raw := buildApk(t, dir, "-apk-icon", icon)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if off, err := f.DataOffset(); err != nil || off%4 != 0 || f.Method != zip.Store {
			t.Errorf("%s: method %d at %d, want stored at a multiple of 4 (%v)", f.Name, f.Method, off, err)
		}
	}
	if got := strings.Join(names, " "); got != "AndroidManifest.xml resources.arsc res/drawable/icon.png lib/arm64-v8a/libapp.so lib/x86_64/libapp.so" {
		t.Errorf("entries = %s", got)
	}
	if got := readZipEntry(t, zr, "res/drawable/icon.png"); !bytes.Equal(got, buf.Bytes()) {
		t.Error("the icon in the APK is not the PNG of -apk-icon")
	}
	table := readZipEntry(t, zr, "resources.arsc")
	if len(table)%4 != 0 || binary.LittleEndian.Uint32(table[4:]) != uint32(len(table)) {
		t.Errorf("the resource table has %d bytes and gives the size %d", len(table), binary.LittleEndian.Uint32(table[4:]))
	}
	for _, s := range []string{"res/drawable/icon.png", "drawable", "icon", "dev.fern.app"} {
		if !bytes.Contains(table, utf16le(s)) {
			t.Errorf("the resource table has no string %q", s)
		}
	}
	verifyApkV2(t, raw)
	aapt, err := exec.LookPath("aapt")
	if err != nil {
		t.Skip("aapt is not on PATH, so the resource table is not parsed")
	}
	o, err := exec.Command(aapt, "dump", "badging", path).CombinedOutput()
	if err != nil || !strings.Contains(string(o), "application: label='app' icon='res/drawable/icon.png'") ||
		!strings.Contains(string(o), "launchable-activity: name='android.app.NativeActivity'  label='app' icon='res/drawable/icon.png'") {
		t.Errorf("aapt dump badging: %v\n%s", err, o)
	}
}

func readZipEntry(t *testing.T, zr *zip.Reader, name string) []byte {
	t.Helper()
	f, err := zr.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(f); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func utf16le(s string) []byte {
	var out []byte
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

// verifyApkV2 checks the APK Signature Scheme v2 block as Android does: the
// chunked digest of the entries, the central directory and the end record,
// and the ECDSA signature of the signed data with the certificate's key.
func verifyApkV2(t *testing.T, raw []byte) {
	t.Helper()
	eocd := len(raw) - 22
	if binary.LittleEndian.Uint32(raw[eocd:]) != 0x06054b50 {
		t.Fatal("no end of central directory record at the end")
	}
	cdOff := int(binary.LittleEndian.Uint32(raw[eocd+16:]))
	if string(raw[cdOff-16:cdOff]) != "APK Sig Block 42" {
		t.Fatal("no APK Signing Block before the central directory")
	}
	size := int(binary.LittleEndian.Uint64(raw[cdOff-24:]))
	start := cdOff - size - 8
	if int(binary.LittleEndian.Uint64(raw[start:])) != size {
		t.Fatal("the two sizes of the signing block differ")
	}
	pairs := raw[start+8 : cdOff-24]
	var v2 []byte
	for len(pairs) > 0 {
		n := int(binary.LittleEndian.Uint64(pairs))
		if binary.LittleEndian.Uint32(pairs[8:]) == 0x7109871a {
			v2 = pairs[12 : 8+n]
		}
		pairs = pairs[8+n:]
	}
	if v2 == nil {
		t.Fatal("no v2 block")
	}
	signers, _ := lp(t, v2)
	signer, _ := lp(t, signers)
	signed, rest := lp(t, signer)
	sigs, rest := lp(t, rest)
	pub, _ := lp(t, rest)
	digests, srest := lp(t, signed)
	certs, _ := lp(t, srest)
	cert, _ := lp(t, certs)
	c, err := x509.ParseCertificate(cert)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	key, ok := c.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("the certificate key is %T", c.PublicKey)
	}
	spki, err := x509.MarshalPKIXPublicKey(key)
	if err != nil || !bytes.Equal(spki, pub) {
		t.Error("the signer's public key is not the certificate's")
	}
	if err := c.CheckSignature(x509.ECDSAWithSHA256, c.RawTBSCertificate, c.Signature); err != nil {
		t.Errorf("the certificate is not self-signed: %v", err)
	}
	sig, _ := lp(t, sigs)
	if binary.LittleEndian.Uint32(sig) != 0x0201 {
		t.Fatalf("signature algorithm %#x, want ECDSA with SHA-256", binary.LittleEndian.Uint32(sig))
	}
	sigBytes, _ := lp(t, sig[4:])
	h := sha256.Sum256(signed)
	if !ecdsa.VerifyASN1(key, h[:], sigBytes) {
		t.Error("the v2 signature does not verify")
	}
	dg, _ := lp(t, digests)
	want, _ := lp(t, dg[4:])
	end := append([]byte{}, raw[eocd:]...)
	binary.LittleEndian.PutUint32(end[16:], uint32(start))
	if got := v2Digest(raw[:start], raw[cdOff:eocd], end); !bytes.Equal(got, want) {
		t.Errorf("content digest %x, want %x", got, want)
	}
}

func lp(t *testing.T, b []byte) ([]byte, []byte) {
	t.Helper()
	if len(b) < 4 {
		t.Fatal("a length-prefixed field is cut short")
	}
	n := int(binary.LittleEndian.Uint32(b))
	if 4+n > len(b) {
		t.Fatalf("a field of %d bytes in %d", n, len(b)-4)
	}
	return b[4 : 4+n], b[4+n:]
}

func v2Digest(sections ...[]byte) []byte {
	var chunks []byte
	count := 0
	for _, s := range sections {
		for at := 0; at < len(s); at += 1 << 20 {
			end := min(at+1<<20, len(s))
			h := sha256.New()
			var head [5]byte
			head[0] = 0xa5
			binary.LittleEndian.PutUint32(head[1:], uint32(end-at))
			h.Write(head[:])
			h.Write(s[at:end])
			chunks = h.Sum(chunks)
			count++
		}
	}
	var head [5]byte
	head[0] = 0x5a
	binary.LittleEndian.PutUint32(head[1:], uint32(count))
	h := sha256.New()
	h.Write(head[:])
	h.Write(chunks)
	return h.Sum(nil)
}

// An app with @jni classes: fern -apk writes them to classes.dex, the
// manifest says hasCode, and the class that extends NativeActivity is the
// activity. TestApkWriter's app has no @jni class, and so no classes.dex.
func TestApkJniClasses(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "dextest.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out, "-apk-package", "dev.fern.dextest",
		e2eharness.RepoPath("tests/android/dex", "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	classes := readZipEntry(t, zr, "classes.dex")
	checkDex(t, classes)
	for _, s := range []string{"Ldev/fern/dextest/Main;", "Landroid/app/NativeActivity;", "Ldev/fern/dextest/Ping;", "Ljava/lang/Runnable;", "loadLibrary", "app", "fern"} {
		if !bytes.Contains(classes, []byte(s)) {
			t.Errorf("classes.dex has no string %q", s)
		}
	}
	man := readZipEntry(t, zr, "AndroidManifest.xml")
	if !bytes.Contains(man, utf16le("dev.fern.dextest.Main")) {
		t.Error("the manifest does not name dev.fern.dextest.Main as the activity")
	}
	verifyApkV2(t, raw)
	if dx, err := filepath.Glob("/usr/lib/android-sdk/build-tools/*/lib/dx.jar"); err == nil && len(dx) > 0 {
		if java, err := exec.LookPath("java"); err == nil {
			dexFile := filepath.Join(dir, "classes.dex")
			if err := os.WriteFile(dexFile, classes, 0o644); err != nil {
				t.Fatal(err)
			}
			o, err := exec.Command(java, "-cp", dx[0], "com.android.dx.merge.DexMerger", filepath.Join(dir, "merged.dex"), dexFile).CombinedOutput()
			if err != nil {
				t.Errorf("DexMerger cannot read classes.dex: %v\n%s", err, o)
			}
		}
	}
	if aapt, err := exec.LookPath("aapt"); err == nil {
		o, _ := exec.Command(aapt, "dump", "badging", out).CombinedOutput()
		if !strings.Contains(string(o), "launchable-activity: name='dev.fern.dextest.Main'") {
			t.Errorf("aapt dump badging names another activity:\n%s", o)
		}
	}
}

// A @jni class can implement a nested interface: classes.dex names it with
// its "$" form, as Android declares it.
func TestApkNestedInterface(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "focustest.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out, "-apk-package", "dev.fern.focustest", "-apk-min-sdk", "28",
		e2eharness.RepoPath("tests/android/focus", "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	classes := readZipEntry(t, zr, "classes.dex")
	checkDex(t, classes)
	for _, s := range []string{"Ldev/fern/focustest/Listener;", "Landroid/media/AudioManager$OnAudioFocusChangeListener;", "onAudioFocusChange"} {
		if !bytes.Contains(classes, []byte(s)) {
			t.Errorf("classes.dex has no string %q", s)
		}
	}
}

// A @jni class that extends Service is a service in the manifest. It is not
// exported, and its foreground service types are those of the
// FOREGROUND_SERVICE_<TYPE> permissions: MICROPHONE and CONNECTED_DEVICE
// give 0x80 | 0x10.
func TestApkService(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "servicetest.apk")
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-apk", out, "-apk-package", "dev.fern.servicetest",
		"-apk-permission", "RECORD_AUDIO,FOREGROUND_SERVICE,FOREGROUND_SERVICE_MICROPHONE,FOREGROUND_SERVICE_CONNECTED_DEVICE",
		e2eharness.RepoPath("tests/android/service", "app.fern"), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fern -apk: %v\n%s", err, o)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	classes := readZipEntry(t, zr, "classes.dex")
	checkDex(t, classes)
	for _, s := range []string{"Ldev/fern/servicetest/Talk;", "Landroid/app/Service;", "onStartCommand", "onBind"} {
		if !bytes.Contains(classes, []byte(s)) {
			t.Errorf("classes.dex has no string %q", s)
		}
	}
	man := readZipEntry(t, zr, "AndroidManifest.xml")
	for _, s := range []string{"service", "dev.fern.servicetest.Talk", "foregroundServiceType", "android.app.NativeActivity"} {
		if !bytes.Contains(man, utf16le(s)) {
			t.Errorf("the manifest has no string %q", s)
		}
	}
	verifyApkV2(t, raw)
	aapt, err := exec.LookPath("aapt")
	if err != nil {
		t.Skip("aapt is not on PATH, so the manifest tree is not checked")
	}
	o, err := exec.Command(aapt, "dump", "xmltree", out, "AndroidManifest.xml").CombinedOutput()
	if err != nil {
		t.Fatalf("aapt dump xmltree: %v\n%s", err, o)
	}
	tree := string(o)
	service := strings.Index(tree, "E: service")
	if service < 0 {
		t.Fatalf("the manifest has no service element:\n%s", tree)
	}
	for _, want := range []string{
		`A: android:name(0x01010003)="dev.fern.servicetest.Talk"`,
		"A: android:exported(0x01010010)=(type 0x12)0x0",
		"A: android:foregroundServiceType(0x01010599)=(type 0x11)0x90",
	} {
		if !strings.Contains(tree[service:], want) {
			t.Errorf("the service element has no %s:\n%s", want, tree[service:])
		}
	}
	if strings.Index(tree, "E: activity") > service {
		t.Errorf("the service comes before the activity:\n%s", tree)
	}
}

// checkDex checks the header of a DEX file: the magic, the size, the
// SHA-1 signature and the Adler-32 checksum.
func checkDex(t *testing.T, b []byte) {
	t.Helper()
	if len(b) < 0x70 || string(b[:8]) != "dex\n035\x00" {
		t.Fatalf("classes.dex has no DEX 035 magic")
	}
	if got := binary.LittleEndian.Uint32(b[32:]); int(got) != len(b) {
		t.Errorf("the header gives the size %d, and the file has %d bytes", got, len(b))
	}
	if sum := sha1.Sum(b[32:]); !bytes.Equal(sum[:], b[12:32]) {
		t.Error("the SHA-1 signature does not match")
	}
	if got := binary.LittleEndian.Uint32(b[8:]); got != adler32.Checksum(b[12:]) {
		t.Error("the Adler-32 checksum does not match")
	}
}
