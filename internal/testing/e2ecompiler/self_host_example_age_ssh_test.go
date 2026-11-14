package e2ecompiler

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ageSSHKeys copies the SSH keys of examples/age/testdata/ssh into dir. The
// keys come from Python's cryptography package. The encrypted ones use
// bcrypt_pbkdf and aes256-ctr, as ssh-keygen makes them, with the
// passphrase "ssh pass".
func ageSSHKeys(t *testing.T, dir string) {
	t.Helper()
	src, err := filepath.Abs("../../../examples/age/testdata/ssh")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func ageRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ageSSHString is an SSH wire string.
func ageSSHString(b []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...)
}

// ageSSH checks ssh-ed25519 and ssh-rsa recipients and identities against
// age 1.3.2.
func ageSSH(t *testing.T, oracle, port ageTool, dir string) {
	ageSSHKeys(t, dir)
	plain := ageRandom(t, 70000)
	ageWrite(t, filepath.Join(dir, "plain"), plain)
	for _, key := range []string{"ed25519", "rsa2048", "rsa3072"} {
		pub := strings.TrimSpace(ageRead(t, filepath.Join(dir, key+".pub")))
		for _, from := range [][]string{{"-R", key + ".pub"}, {"-r", pub}} {
			sealed, stderr, code := oracle.run(t, dir, plain, from...)
			if code != 0 {
				t.Fatalf("age %v: exit %d %s", from, code, stderr)
			}
			ageWrite(t, filepath.Join(dir, "s.age"), sealed)
			ids := []string{key}
			if key != "rsa3072" {
				ids = append(ids, key+"_pkcs8")
			}
			if key == "rsa2048" {
				ids = append(ids, "rsa2048_pkcs1")
			}
			for _, id := range ids {
				if got, stderr, code := port.run(t, dir, nil, "-d", "-i", id, "s.age"); code != 0 || !bytes.Equal(got, plain) {
					t.Errorf("fern age -d -i %s of age %v: exit %d %s", id, from, code, stderr)
				}
			}
			sealed, stderr, code = port.run(t, dir, plain, from...)
			if code != 0 {
				t.Fatalf("fern age %v: exit %d %s", from, code, stderr)
			}
			if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", key); code != 0 || !bytes.Equal(got, plain) {
				t.Errorf("age -d -i %s of fern age %v: exit %d %s", key, from, code, stderr)
			}
		}
		sealed, stderr, code := port.run(t, dir, plain, "-e", "-i", key, "-a")
		if code != 0 {
			t.Fatalf("fern age -e -i %s: exit %d %s", key, code, stderr)
		}
		if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", key); code != 0 || !bytes.Equal(got, plain) {
			t.Errorf("age -d -i %s of fern age -e -i: exit %d %s", key, code, stderr)
		}
	}
	// All three recipient types in one file: each key opens it.
	ageWrite(t, filepath.Join(dir, "mixed.txt"), []byte(ageRead(t, filepath.Join(dir, "ed25519.pub"))+"# rsa\n"+ageRead(t, filepath.Join(dir, "rsa2048.pub"))))
	sealed, stderr, code := port.run(t, dir, plain, "-R", "mixed.txt", "-r", strings.TrimSpace(ageRead(t, filepath.Join(dir, "rsa3072.pub"))))
	if code != 0 {
		t.Fatalf("fern age -R mixed.txt: exit %d %s", code, stderr)
	}
	for _, key := range []string{"ed25519", "rsa2048", "rsa3072"} {
		if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", key); code != 0 || !bytes.Equal(got, plain) {
			t.Errorf("age -d -i %s of the mixed file: exit %d %s", key, code, stderr)
		}
	}
	ageSSHEncrypted(t, oracle, port, dir, plain)
	ageSSHMessages(t, oracle, port, dir)
}

// ageSSHEncrypted checks the keys that need a passphrase: age asks for it
// only when a stanza is for the key.
func ageSSHEncrypted(t *testing.T, oracle, port ageTool, dir string, plain []byte) {
	for _, key := range []string{"ed25519_encrypted", "rsa_encrypted"} {
		sealed, stderr, code := oracle.run(t, dir, plain, "-R", key+".pub")
		if code != 0 {
			t.Fatalf("age -R %s.pub: exit %d %s", key, code, stderr)
		}
		ageWrite(t, filepath.Join(dir, key+".age"), sealed)
		if got, stderr, _, code := port.runTTY(t, dir, nil, []string{"ssh pass"}, "-d", "-i", key, key+".age"); code != 0 || !bytes.Equal(got, plain) {
			t.Errorf("fern age -d -i %s: exit %d %s", key, code, stderr)
		}
	}
	sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-R", "ed25519.pub")
	ageWrite(t, filepath.Join(dir, "other.age"), sealed)
	// A key in the old PEM format that needs a passphrase: age reads the
	// public key from the .pub file beside it.
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(k), []byte("pem pass"), x509.PEMCipherAES128)
	if err != nil {
		t.Fatal(err)
	}
	ageWrite(t, filepath.Join(dir, "legacy"), pem.EncodeToMemory(block))
	ageWrite(t, filepath.Join(dir, "legacy_nopub"), pem.EncodeToMemory(block))
	pub := append(ageSSHString([]byte("ssh-rsa")), ageSSHString(ageBigEndian(k.E))...)
	pub = append(pub, ageSSHString(append([]byte{0}, k.N.Bytes()...))...)
	ageWrite(t, filepath.Join(dir, "legacy.pub"), []byte("ssh-rsa "+base64.StdEncoding.EncodeToString(pub)+" legacy\n"))
	cases := []struct {
		lines []string
		args  []string
	}{
		{[]string{"wrong"}, []string{"-d", "-i", "ed25519_encrypted", "ed25519_encrypted.age"}},
		{[]string{"wrong"}, []string{"-d", "-i", "rsa_encrypted", "rsa_encrypted.age"}},
		{nil, []string{"-d", "-i", "ed25519_encrypted", "other.age"}},
		{[]string{"ssh pass"}, []string{"-d", "-i", "ed25519_encrypted", "-i", "rsa_encrypted", "rsa_encrypted.age"}},
		{nil, []string{"-e", "-i", "ed25519_encrypted", "-o", "x.age", "plain"}},
		{nil, []string{"-e", "-i", "legacy", "-o", "x.age", "plain"}},
		{nil, []string{"-e", "-i", "legacy_nopub", "-o", "x.age", "plain"}},
		{nil, []string{"-d", "-i", "legacy", "other.age"}},
	}
	for _, tc := range cases {
		_, want, wantScreen, wantCode := oracle.runTTY(t, dir, nil, tc.lines, tc.args...)
		_, got, screen, code := port.runTTY(t, dir, nil, tc.lines, tc.args...)
		if code != wantCode || got != want || screen != wantScreen {
			t.Errorf("%v %q: age exit %d %q %q; fern age exit %d %q %q", tc.args, tc.lines, wantCode, want, wantScreen, code, got, screen)
		}
	}
	for _, args := range [][]string{{"-d", "-i", "ed25519_encrypted", "ed25519_encrypted.age"}} {
		want, wantCode := oracle.runDetached(t, dir, nil, args...)
		got, code := port.runDetached(t, dir, nil, args...)
		if code != wantCode || got != want {
			t.Errorf("%v with no terminal: age exit %d %q; fern age exit %d %q", args, wantCode, want, code, got)
		}
	}
}

// ageBigEndian is the minimal big-endian form of a small positive int.
func ageBigEndian(v int) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(v))
	for len(b) > 1 && b[0] == 0 {
		b = b[1:]
	}
	return b
}

// ageSSHMessages checks age's message and exit code for malformed SSH keys,
// stanzas and command lines.
func ageSSHMessages(t *testing.T, oracle, port ageTool, dir string) {
	ed := strings.TrimSpace(ageRead(t, filepath.Join(dir, "ed25519.pub")))
	edKey := strings.Fields(ed)[1]
	// x/crypto/ssh parses a DSA key only when p has 1024 bits.
	dssKey := func(p []byte) string {
		dss := append(ageSSHString([]byte("ssh-dss")), ageSSHString(p)...)
		for range 3 {
			dss = append(dss, ageSSHString([]byte{3})...)
		}
		return "ssh-dss " + base64.StdEncoding.EncodeToString(dss)
	}
	dssLine := dssKey(append([]byte{0, 0x80}, ageRandom(t, 127)...))
	dssShort := dssKey([]byte{1, 2})
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"rf-ssh":       "# keys\n" + ed + "\n" + ageRead(t, filepath.Join(dir, "rsa1024.pub")) + dssLine + "\n",
		"rf-small":     ageRead(t, filepath.Join(dir, "rsa1024.pub")),
		"bad-pem":      "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n",
		"bad-type":     "-----BEGIN FOO-----\nAAAA\n-----END FOO-----\n",
		"no-pem":       "-----BEGIN nothing here\n",
		"pub-as-id":    ed + "\n",
		"ec-key":       string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER})),
		"long-pem":     "-----BEGIN OPENSSH PRIVATE KEY-----\n" + strings.Repeat("A", 17000) + "\n",
		"ed-truncated": strings.Replace(ageRead(t, filepath.Join(dir, "ed25519")), "AAAAAECAwQF\n", "AAAAAECAw\n", 1),
	}
	for name, content := range files {
		ageWrite(t, filepath.Join(dir, name), []byte(content))
	}
	sealedEd, _, _ := oracle.run(t, dir, []byte("hello"), "-R", "ed25519.pub")
	sealedRSA, _, _ := oracle.run(t, dir, []byte("hello"), "-R", "rsa2048.pub")
	ageWrite(t, filepath.Join(dir, "ed.age"), sealedEd)
	ageWrite(t, filepath.Join(dir, "rsa.age"), sealedRSA)
	lines := strings.Split(string(sealedEd), "\n")
	edArgs := strings.Fields(lines[1])
	edBody := lines[2]
	ageWrite(t, filepath.Join(dir, "ed-tag.age"), []byte(strings.Replace(string(sealedEd), edBody, edBody[:10]+map[bool]string{true: "B", false: "A"}[edBody[10] == 'A']+edBody[11:], 1)))
	ageWrite(t, filepath.Join(dir, "ed-args.age"), []byte(strings.Replace(string(sealedEd), lines[1], strings.Join(edArgs[:3], " "), 1)))
	ageWrite(t, filepath.Join(dir, "ed-share.age"), []byte(strings.Replace(string(sealedEd), edArgs[3], "AAAA", 1)))
	ageWrite(t, filepath.Join(dir, "ed-low.age"), []byte(strings.Replace(string(sealedEd), edArgs[3], base64.RawStdEncoding.EncodeToString(make([]byte, 32)), 1)))
	rsaLines := strings.Split(string(sealedRSA), "\n")
	ageWrite(t, filepath.Join(dir, "rsa-body.age"), []byte(strings.Replace(string(sealedRSA), rsaLines[2], "A"+rsaLines[2][1:], 1)))
	ageWrite(t, filepath.Join(dir, "rsa-args.age"), []byte(strings.Replace(string(sealedRSA), rsaLines[1], rsaLines[1]+" x", 1)))
	cases := [][]string{
		{"-r", "ssh-ed25519 " + edKey[:20], "plain"},
		{"-r", "ssh-ed25519", "plain"},
		{"-r", "ssh-ed25519 AAAA", "plain"},
		{"-r", strings.TrimSpace(ageRead(t, filepath.Join(dir, "rsa1024.pub"))), "plain"},
		{"-r", dssLine, "plain"},
		{"-r", dssShort, "plain"},
		{"-r", "ssh-foo AAAAB3NzaC1mb28=", "plain"},
		{"-r", "no-pty " + ed, "plain"},
		{"-R", "rf-ssh", "-o", "x.age", "plain"},
		{"-R", "rf-small", "plain"},
		{"-d", "-i", "bad-pem", "ed.age"},
		{"-d", "-i", "bad-type", "ed.age"},
		{"-d", "-i", "no-pem", "ed.age"},
		{"-d", "-i", "pub-as-id", "ed.age"},
		{"-d", "-i", "ec-key", "ed.age"},
		{"-d", "-i", "long-pem", "ed.age"},
		{"-d", "-i", "ed-truncated", "ed.age"},
		{"-d", "-i", "ed25519_other", "ed.age"},
		{"-d", "-i", "rsa2048", "ed.age"},
		{"-d", "-i", "ed25519", "rsa.age"},
		{"-d", "-i", "ed25519", "ed-tag.age"},
		{"-d", "-i", "ed25519", "ed-args.age"},
		{"-d", "-i", "ed25519", "ed-share.age"},
		{"-d", "-i", "ed25519", "ed-low.age"},
		{"-d", "-i", "rsa2048", "rsa-body.age"},
		{"-d", "-i", "rsa2048", "rsa-args.age"},
		{"-d", "-i", "ed25519_other", "-i", "ed25519", "ed.age"},
	}
	for _, args := range cases {
		_, want, wantCode := oracle.run(t, dir, []byte("hello"), args...)
		_, got, gotCode := port.run(t, dir, []byte("hello"), args...)
		if gotCode != wantCode || got != want {
			t.Errorf("age %s: age exit %d %q; fern age exit %d %q", strings.Join(args, " "), wantCode, want, gotCode, got)
		}
	}
}

var ageKeygenOutput = regexp.MustCompile(`^# created: (\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:Z|[+-]\d\d:\d\d))\n# public key: (age1[02-9ac-hj-np-z]{58})\n(AGE-SECRET-KEY-1[02-9AC-HJ-NP-Z]{58})\n$`)

var ageKeygenHybridOutput = regexp.MustCompile(`^# created: [^\n]+\n# public key: (age1pq1[02-9ac-hj-np-z]+)\n(AGE-SECRET-KEY-PQ-1[02-9AC-HJ-NP-Z]{58})\n$`)

// ageKeygenHybrid checks -pq: age decrypts with the hybrid key of the port,
// and the two tools give the same recipients for each other's keys.
func ageKeygenHybrid(t *testing.T, oracle, keygen, port ageTool, dir string) {
	out, stderr, code := port.run(t, dir, nil, "-pq")
	m := ageKeygenHybridOutput.FindStringSubmatch(string(out))
	if code != 0 || m == nil || len(m[1]) != 1959 || stderr != "Public key: "+m[1]+"\n" {
		t.Fatalf("fern age-keygen -pq: exit %d %q %q", code, out, stderr)
	}
	ageWrite(t, filepath.Join(dir, "pq"), out)
	if r, _, _ := keygen.run(t, dir, nil, "-y", "pq"); strings.TrimSpace(string(r)) != m[1] {
		t.Errorf("age-keygen -y of the fern age-keygen -pq key gives %q, want %q", r, m[1])
	}
	sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-r", m[1])
	if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", "pq"); code != 0 || string(got) != "hello" {
		t.Errorf("age -d with the -pq key of fern age-keygen: exit %d %s", code, stderr)
	}
	os.Remove(filepath.Join(dir, "pq-age"))
	if _, stderr, code := keygen.run(t, dir, nil, "-pq", "-o", "pq-age"); code != 0 {
		t.Fatalf("age-keygen -pq: exit %d %s", code, stderr)
	}
	want, _, _ := keygen.run(t, dir, nil, "-y", "pq-age")
	if got, stderr, code := port.run(t, dir, nil, "-y", "pq-age"); code != 0 || !bytes.Equal(got, want) {
		t.Errorf("fern age-keygen -y of an age-keygen -pq key: exit %d %q %s, want %q", code, got, stderr, want)
	}
	ageWrite(t, filepath.Join(dir, "mixed"), []byte(ageRead(t, filepath.Join(dir, "pq"))+ageRead(t, filepath.Join(dir, "k"))))
	want, _, _ = keygen.run(t, dir, nil, "-y", "mixed")
	if got, _, code := port.run(t, dir, nil, "-y", "mixed"); code != 0 || !bytes.Equal(got, want) {
		t.Errorf("fern age-keygen -y of a hybrid and an X25519 key: %q, want %q", got, want)
	}
}

// TestSelfHostExampleAgeKeygen checks the age-keygen port against
// age-keygen 1.3.2 on x86-64 and arm64.
func TestSelfHostExampleAgeKeygen(t *testing.T) {
	oracle, keygenPath := ageOracle(t)
	keygen := ageTool{name: "age-keygen", cmd: func(args ...string) *exec.Cmd { return exec.Command(keygenPath, args...) }}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := ageProgram(t, cli, target, "keygen.fern")
			dir := t.TempDir()
			ageKeygen(t, oracle, keygen, port, dir)
		})
	}
}

func ageKeygen(t *testing.T, oracle, keygen, port ageTool, dir string) {
	for _, tz := range []string{"UTC", "Asia/Kolkata", "America/New_York"} {
		t.Setenv("TZ", tz)
		name := "want-" + strings.ReplaceAll(tz, "/", "-")
		_, wantErr, _ := keygen.run(t, dir, nil, "-o", name)
		out, stderr, code := port.run(t, dir, nil)
		m := ageKeygenOutput.FindStringSubmatch(string(out))
		if code != 0 || m == nil || stderr != "Public key: "+m[2]+"\n" {
			t.Fatalf("fern age-keygen in %s: exit %d %q %q", tz, code, out, stderr)
		}
		want := ageKeygenOutput.FindStringSubmatch(ageRead(t, filepath.Join(dir, name)))
		if want == nil || wantErr != "Public key: "+want[2]+"\n" {
			t.Fatalf("age-keygen in %s: %q", tz, wantErr)
		}
		got, err := time.Parse(time.RFC3339, m[1])
		ref, err2 := time.Parse(time.RFC3339, want[1])
		if err != nil || err2 != nil || got.Sub(ref).Abs() > time.Minute || m[1][19:] != want[1][19:] {
			t.Errorf("TZ=%s: fern age-keygen wrote %s, age-keygen %s", tz, m[1], want[1])
		}
		ageWrite(t, filepath.Join(dir, "k"), out)
		if r, _, _ := keygen.run(t, dir, nil, "-y", "k"); strings.TrimSpace(string(r)) != m[2] {
			t.Errorf("age-keygen -y of the fern age-keygen key gives %q, want %q", r, m[2])
		}
		sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-r", m[2])
		if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", "k"); code != 0 || string(got) != "hello" {
			t.Errorf("age -d with the key of fern age-keygen: exit %d %s", code, stderr)
		}
	}
	ageKeygenHybrid(t, oracle, keygen, port, dir)
	os.Remove(filepath.Join(dir, "new"))
	if _, stderr, code := port.run(t, dir, nil, "-o", "new"); code != 0 || !strings.HasPrefix(stderr, "Public key: age1") {
		t.Fatalf("fern age-keygen -o: exit %d %s", code, stderr)
	}
	if st, err := os.Stat(filepath.Join(dir, "new")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("fern age-keygen -o made a file with mode %v, want 0600", st.Mode().Perm())
	}
	ageWrite(t, filepath.Join(dir, "two"), []byte("# two keys\n"+ageRead(t, filepath.Join(dir, "k"))+"\r\n"+ageRead(t, filepath.Join(dir, "new"))))
	for name, content := range map[string]string{"empty": "", "garbage": "hello\n", "lower": strings.ToLower(ageRead(t, filepath.Join(dir, "new"))), "ssh": "ssh-ed25519 AAAA\n"} {
		ageWrite(t, filepath.Join(dir, name), []byte(content))
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-y", "two"}, {"-y", "-o", "y-out", "two"}, {"-y", "empty"}, {"-y", "garbage"}, {"-y", "lower"}, {"-y", "ssh"}, {"-y", "nosuch"}, {"-y", "sub"},
		{"-y", "a", "b"}, {"x"}, {"-o", "new"}, {"-o", "nodir/x"}, {"--bogus"}, {"-o"}, {"-y=maybe"}, {"-h"},
		{"-pq", "-y", "two"}, {"-y", "-pq"}, {"-pq", "x"}, {"-pq=false", "-o", "new"}, {"-y", "-o", "y-out", "garbage"}, {"-y", "-o", "new", "nosuch"},
	} {
		os.Remove(filepath.Join(dir, "y-out"))
		wantOut, want, wantCode := keygen.run(t, dir, nil, args...)
		wantFile, _ := os.ReadFile(filepath.Join(dir, "y-out"))
		os.Remove(filepath.Join(dir, "y-out"))
		gotOut, got, gotCode := port.run(t, dir, nil, args...)
		gotFile, _ := os.ReadFile(filepath.Join(dir, "y-out"))
		if wantCode != 0 {
			want = strings.SplitN(strings.ReplaceAll(want, "age-keygen: report unexpected or unhelpful errors at https://filippo.io/age/report\n", ""), "\n", 2)[0]
			got = strings.SplitN(got, "\n", 2)[0]
		}
		if gotCode != wantCode || got != want && args[0] != "-h" || !bytes.Equal(gotOut, wantOut) || !bytes.Equal(gotFile, wantFile) {
			t.Errorf("age-keygen %v: exit %d %q %q; fern age-keygen exit %d %q %q", args, wantCode, want, wantOut, gotCode, got, gotOut)
		}
	}
	// stdin, and a world-readable output file.
	k := []byte(ageRead(t, filepath.Join(dir, "k")))
	want, _, _ := keygen.run(t, dir, k, "-y")
	if got, stderr, code := port.run(t, dir, k, "-y", "-"); code != 0 || !bytes.Equal(got, want) {
		t.Errorf("fern age-keygen -y -: exit %d %s", code, stderr)
	}
	f, err := os.OpenFile(filepath.Join(dir, "readable"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := port.cmd()
	cmd.Dir, cmd.Stdout = dir, f
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || !strings.HasPrefix(stderr.String(), "age-keygen: warning: writing secret key to a world-readable file\nPublic key: ") {
		t.Errorf("fern age-keygen to a world-readable file: %v %q", err, &stderr)
	}
}
