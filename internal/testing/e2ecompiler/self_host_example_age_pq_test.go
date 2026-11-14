package e2ecompiler

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ageHybrid checks the ML-KEM-768 + X25519 recipients (mlkem768x25519)
// against age: each tool decrypts what the other encrypts, the two tools
// refuse a mix of hybrid and classic recipients with the same message, and
// they give the same message for malformed keys and stanzas.
func ageHybrid(t *testing.T, oracle, port ageTool, dir, keygen, r1 string) {
	pq1 := ageHybridKey(t, keygen, filepath.Join(dir, "pq1"))
	pq2 := ageHybridKey(t, keygen, filepath.Join(dir, "pq2"))
	ageWrite(t, filepath.Join(dir, "pq-recipients"), []byte("# a hybrid key\n"+pq2+"\n"))
	for _, size := range []int{0, 1, 65536, 1<<20 + 3} {
		plain := ageRandom(t, size)
		ageWrite(t, filepath.Join(dir, "pq-plain"), plain)
		for _, armor := range [][]string{nil, {"-a"}} {
			name := fmt.Sprintf("%d%v", size, armor)
			sealed, stderr, code := oracle.run(t, dir, plain, append([]string{"-r", pq1}, armor...)...)
			if code != 0 {
				t.Fatalf("age -r age1pq1... %s: exit %d: %s", name, code, stderr)
			}
			if got, stderr, code := port.run(t, dir, sealed, "-d", "-i", "pq1"); code != 0 || !bytes.Equal(got, plain) {
				t.Errorf("fern age -d of age's hybrid file %s: exit %d: %s", name, code, stderr)
			}
			args := append([]string{"-r", pq1, "-R", "pq-recipients", "-o", "pq.age"}, armor...)
			if _, stderr, code := port.run(t, dir, nil, append(args, "pq-plain")...); code != 0 || stderr != "" {
				t.Fatalf("fern age -r age1pq1... %s: exit %d: %s", name, code, stderr)
			}
			ours, err := os.ReadFile(filepath.Join(dir, "pq.age"))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"pq1", "pq2"} {
				if got, stderr, code := oracle.run(t, dir, ours, "-d", "-i", key); code != 0 || !bytes.Equal(got, plain) {
					t.Errorf("age -d -i %s of fern age %s: exit %d: %s", key, name, code, stderr)
				}
			}
		}
	}
	sealed, stderr, code := port.run(t, dir, []byte("to myself\n"), "-e", "-i", "pq1")
	if code != 0 {
		t.Fatalf("fern age -e -i with a hybrid key: exit %d: %s", code, stderr)
	}
	if got, stderr, code := oracle.run(t, dir, sealed, "-d", "-i", "pq1"); code != 0 || string(got) != "to myself\n" {
		t.Errorf("age -d of fern age -e -i with a hybrid key: exit %d: %s", code, stderr)
	}
	ageHybridMessages(t, oracle, port, dir, r1, pq1)
}

// ageHybridKey writes a new hybrid identity file with age-keygen -pq and
// gives its recipient.
func ageHybridKey(t *testing.T, keygen, path string) string {
	t.Helper()
	tool := ageTool{name: "age-keygen", cmd: func(args ...string) *exec.Cmd { return exec.Command(keygen, args...) }}
	if _, stderr, code := tool.run(t, filepath.Dir(path), nil, "-pq", "-o", path); code != 0 {
		t.Fatalf("age-keygen -pq: exit %d: %s", code, stderr)
	}
	out, _, code := tool.run(t, filepath.Dir(path), nil, "-y", path)
	if code != 0 {
		t.Fatalf("age-keygen -y %s: exit %d", path, code)
	}
	return strings.TrimSpace(string(out))
}

func ageHybridMessages(t *testing.T, oracle, port ageTool, dir, r1, pq1 string) {
	sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-r", pq1)
	key := ""
	for _, line := range strings.Split(ageRead(t, filepath.Join(dir, "pq1")), "\n") {
		if strings.HasPrefix(line, "AGE-SECRET-KEY-PQ-1") {
			key = line
		}
	}
	// The stanza: its type and encapsulated key, then its body line.
	lines := strings.Split(string(sealed), "\n")
	arg := strings.TrimPrefix(lines[1], "-> mlkem768x25519 ")
	enc, err := base64.RawStdEncoding.DecodeString(arg)
	if err != nil || len(enc) != 1120 {
		t.Fatalf("age's hybrid stanza: %v, %d bytes", err, len(enc))
	}
	b64 := base64.RawStdEncoding.EncodeToString
	edit := func(old, new string) []byte { return []byte(strings.Replace(string(sealed), old, new, 1)) }
	flipped := append([]byte{}, enc...)
	flipped[0] ^= 1
	lowOrder := append(append([]byte{}, enc[:1088]...), make([]byte, 32)...)
	files := map[string][]byte{
		"pq-ok":            sealed,
		"pq-enc-flip":      edit(arg, b64(flipped)),
		"pq-enc-short":     edit(arg, b64(enc[:1119])),
		"pq-enc-long":      edit(arg, b64(append(append([]byte{}, enc...), 0))),
		"pq-enc-padded":    edit(arg, arg+"="),
		"pq-low-order":     edit(arg, b64(lowOrder)),
		"pq-two-args":      edit(arg, arg+" x"),
		"pq-no-args":       edit(" "+arg, ""),
		"pq-long-body":     edit("\n"+lines[2]+"\n", "\n"+b64(make([]byte, 33))+"\n"),
		"pq-upper":         edit("mlkem768x25519", "MLKEM768X25519"),
		"pq-and-x25519":    edit("-> mlkem768x25519", "-> X25519 "+b64(make([]byte, 32))+"\n"+b64(make([]byte, 32))+"\n-> mlkem768x25519"),
		"pq-grease-before": edit("-> mlkem768x25519", "-> grease x\n\n-> mlkem768x25519"),
	}
	for name, data := range files {
		ageWrite(t, filepath.Join(dir, name), data)
	}
	change := func(s string, i int, c byte) string { b := []byte(s); b[i] = c; return string(b) }
	for name, content := range map[string]string{
		"pq-id-checksum": change(key, len(key)-1, map[bool]byte{true: 'Q', false: 'P'}[key[len(key)-1] == 'P']) + "\n",
		"pq-id-short":    key[:len(key)-1] + "\n",
		"pq-id-lower":    strings.ToLower(key) + "\n",
		"pq-id-and-x":    key + "\n" + ageRead(t, filepath.Join(dir, "k1")),
		"pq-rf":          "# c\n" + pq1 + "\n" + r1 + "\n",
	} {
		ageWrite(t, filepath.Join(dir, name), []byte(content))
	}
	cases := [][]string{
		{"-r", pq1, "-r", r1, "p"},
		{"-r", r1, "-r", pq1, "p"},
		{"-R", "pq-rf", "p"},
		{"-e", "-i", "pq1", "-r", r1, "p"},
		{"-e", "-i", "pq-id-and-x", "p"},
		{"-r", pq1[:len(pq1)-1], "p"},
		{"-r", change(pq1, 10, map[bool]byte{true: 'p', false: 'q'}[pq1[10] == 'q']), "p"},
		{"-r", strings.ToUpper(pq1), "p"},
		{"-r", "age1pq1qqqqqqqqqqqqq", "p"},
		{"-d", "-i", "pq-id-checksum", "pq-ok"},
		{"-d", "-i", "pq-id-short", "pq-ok"},
		{"-d", "-i", "pq-id-lower", "pq-ok"},
		{"-d", "-i", "pq-id-and-x", "pq-ok"},
		{"-d", "-i", "k1", "pq-ok"},
		{"-d", "pq-ok"},
	}
	for name := range files {
		cases = append(cases, []string{"-d", "-i", "pq1", name})
	}
	for _, args := range cases {
		wantOut, want, wantCode := oracle.run(t, dir, nil, args...)
		gotOut, got, gotCode := port.run(t, dir, nil, args...)
		if gotCode != wantCode || got != want || !bytes.Equal(gotOut, wantOut) {
			t.Errorf("age %s: age exit %d %q; fern age exit %d %q", strings.Join(args, " "), wantCode, want, gotCode, got)
		}
	}
}
