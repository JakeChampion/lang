package e2ecompiler

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The recipients of age-plugin-tagtest: DeriveKeyPair of "age-plugin-tagtest"
// with the P-256 KEM and with the MLKEM768-P256 KEM.
const ageTagClassic = "age1tag1qwe0kafsjrar4txm6heqnhpfuggzr0gvznz7fvygxrlq90u5mq2pysxtw6h"

const ageTagHybrid = "age1tagpq14h4z7cks9sxftfc8tq4xektt4854ur9rv76tvujdvtzk2fmyywkvh9z2emz3x4epvhz7qdt2v7uksyyq2cdzf3k04ny0g5sc3u4heqh3r9v4cnwhfjw0a2azpgmnk9xk02wvywt5szcq6q3jvwsjxvkn3tsk52vqjczdcvc398ym4j6cvqas4w99gkgt7ur3fmt4g873phr23tgxw3f7wgsz9zxz7m8cp27vpq3h5vc8nssjemtr2etmtmqkg4fzn2u9x9zvtysuya5yrytgx482ftx9864h8a6pprarxd3d0qe8nw2at5ekg3tsahtef7kawasxjamyckw2ans6v933vuypcfrra32f89r2v72mka9hhc55s49xe2khfsq7w9r2zynuzfx4fg6v7jjncsc87rw2yy8qp8hr27edus6zw5xd6m3hax2nxhl2dys9792z3wp5c034sfkrxe86guj7pfdh7sytzrufl9euhuhyf9w6c7z2nwf7v5v8f2s4gplgvfx4jj4le22k2qn242qkqkcwx7llfyrct7jm2wcv0ytypeh6h93ezgtd7q6zr428qze3dec5jxlc5xxjetyephp42fljft3s02p0570kjwyfeyjcnks2vglkvyus5g9l4z6m0gf8wu22ygfm40028txwjlxvvgnn7c36z783c6tmc9k5nef8nucj6u3ustff5vhtnzhnscsvsz79wzrkv3sujtntx4wezucy6lp49flmnyydn3khk2xsesw0ekn4u44nzqw2g2rjyrrl7crshlzttgpe0jvqycjzp9kmtz23t3yu0w9j4n344nnrf88k2jqqfpjxte38pcn0epr879pqsuvajxrkmsas89pvfrzwcewneujn08guj5pvvrtn5hzzg2y6u4wwjqqxx4x8w65yc4dchf750dft8kgcttt2f6j0j5v8s7tkaua78tte7artdfar544vl0rau79h95mc4ghp887z82s6rq93txpkvan86n963kagqkldngnkjcn28zdrh38vdxj002zqs9mx7zjvg3ynzdfhfakkynt9fyqpaxpsdrsqrycuhw5ykwgjz6wldef7xtu6p689234hstxe7v8e5422f2dy8ystn57z3fvy9yfrm4t3lye6ejk5n6x8zqexmql7lx965xcxuuy38xzyt8j9qprnwgfqgx54l4tnjdpzdde6xgmwtnpkfwvyr7rkgnavvjn6a3e56wtvjx3evmhjjxvukpq5zqrj0s4sntkz3yeszs5dty8q0q6m7dgp6mjpvaer0c4343g72eycfqzkjupeaemh0n8e935hqs8fh3jgk7fzyxctzuqlx6d2q9jaf8r9wu4sjxj5w6ppw7m9c3hxrzpcv2uek3kxnndgf2hd99q9v2ux8pjkv29ntslvnvhy09dvcy9578rt89gf4cj4cu79zjxtlj3dpct6rjme02zj3qspsade96njkkufu9zuq2lk3qwvddpxjkqm2hnpqwck54zug7ctvkgvk325lwkg4q5rf73zkgys5e9y8jqc96ntdyl4r78lgtw4k5uljk5ttf46s3gc0rq0jwmddnxt875twwq92505zh3zkse5ag2dhjjxyfzkn7xv3j0kv9r3jzpvgep8fq6z8mar509u4fvnhvthp2ah0r45lsyq0mm6fwkcs30v8k9wzvgt6uvcty6qsjvarjs3htym69zu43m4jd3k4tllrr8c05v6p6spuhup4hkk2p9fp9lxafe3pntcn4nk83gzhjjpcjwyg7jcyz5uancu0fakgz27up7ymzp2xv3sqyqewkkqynskw9qkvysrncxj0cy7dt6q8dsseuwmc2urfmcvkykf82wfa54t85hqx8gywhmhzunm2x0d66a4pwl0xl78fhkces5dpq8pfnp35m5a3u8vdam64zx5s5x9cmnrx3zr066f4f8hlecqnq2fd5quw79ljg3q5nvs6ggmm4gkc"

// The test plugin's recipient and identity of age's plugin tests.
const ageTestRecipient = "age1test10qdmzv9q"

const ageTestIdentity = "AGE-PLUGIN-TEST-10Q32NLXM"

// agePluginFiles are the plugins of the port, by the name that age looks up.
var agePluginFiles = map[string]string{
	"tag":       "plugin_tag.fern",
	"tagpq":     "plugin_tagpq.fern",
	"pq":        "plugin_pq.fern",
	"batchpass": "plugin_batchpass.fern",
	"test":      "testdata/plugin/test.fern",
	"tagtest":   "testdata/plugin/tagtest.fern",
}

// agePluginDir builds the plugins of the port for target, and writes a
// script age-plugin-NAME for each one in a new directory. With only, the
// directory holds only those plugins.
func agePluginDir(t *testing.T, cli *selfHostCLI, target string, only ...string) string {
	t.Helper()
	dir := t.TempDir()
	for name, file := range agePluginFiles {
		if len(only) > 0 && !strings.Contains(" "+strings.Join(only, " ")+" ", " "+name+" ") {
			continue
		}
		cmd := ageProgram(t, cli, target, file).cmd()
		quoted := make([]string, len(cmd.Args))
		for i, a := range cmd.Args {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		quoted[0] = "'" + cmd.Path + "'"
		script := "#!/bin/sh\nexec " + strings.Join(quoted, " ") + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, "age-plugin-"+name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Polymod(values []byte) uint32 {
	gen := []uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

// ageBech32 encodes data under hrp, as age writes a recipient.
func ageBech32(hrp string, data []byte) string {
	var values []byte
	acc, bits := 0, 0
	for _, b := range data {
		acc = acc<<8 | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			values = append(values, byte(acc>>bits&31))
		}
	}
	if bits > 0 {
		values = append(values, byte(acc<<(5-bits)&31))
	}
	var expanded []byte
	for _, c := range hrp {
		expanded = append(expanded, byte(c>>5))
	}
	expanded = append(expanded, 0)
	for _, c := range hrp {
		expanded = append(expanded, byte(c&31))
	}
	chk := bech32Polymod(append(append(expanded, values...), 0, 0, 0, 0, 0, 0)) ^ 1
	var out strings.Builder
	out.WriteString(hrp + "1")
	for _, v := range values {
		out.WriteByte(bech32Charset[v])
	}
	for i := 0; i < 6; i++ {
		out.WriteByte(bech32Charset[chk>>(5*(5-i))&31])
	}
	return out.String()
}

// ageEnv sets the variables of the plugins for one command and clears the
// others.
func ageEnv(t *testing.T, vars ...string) {
	t.Helper()
	set := map[string]string{"AGE_TEST_PLUGIN": "", "AGE_PASSPHRASE": "", "AGE_PASSPHRASE_FD": "", "AGE_PASSPHRASE_WORK_FACTOR": "", "AGE_PASSPHRASE_MAX_WORK_FACTOR": ""}
	for _, v := range vars {
		k, val, _ := strings.Cut(v, "=")
		set[k] = val
	}
	for k, v := range set {
		os.Setenv(k, v)
	}
}

// ageScryptLabel is the random label of an scrypt recipient in age's
// message for recipients that do not mix.
var ageScryptLabel = regexp.MustCompile(`"[0-9a-f]{32}"`)

// agePlugins checks the plugin protocol against age: each tool encrypts to
// and decrypts with the plugins of the port, the tag recipients and the
// identities of age-plugin-tagtest, and each tool gives the same message for
// plugins that are missing or that break the protocol.
func agePlugins(t *testing.T, oracle, port ageTool, cli *selfHostCLI, target, keygen string) {
	orig := os.Getenv("PATH")
	for _, name := range []string{"pq", "batchpass", "tag", "tagpq"} {
		if _, err := exec.LookPath("age-plugin-" + name); err != nil {
			t.Fatalf("age-plugin-%s is required: it is in the release archive of age %s", name, ageOracleVersion)
		}
	}
	ours := agePluginDir(t, cli, target) + string(os.PathListSeparator) + orig
	tests := agePluginDir(t, cli, target, "test", "tagtest") + string(os.PathListSeparator) + orig
	t.Setenv("PATH", ours)
	// The oracle runs the plugins of age, and the port runs the plugins of
	// the port.
	release := ageTool{name: oracle.name, bin: oracle.bin, cmd: func(args ...string) *exec.Cmd {
		c := oracle.cmd(args...)
		c.Env = append(os.Environ(), "PATH="+tests)
		return c
	}}
	defer ageEnv(t)
	dir := t.TempDir()
	ageWrite(t, filepath.Join(dir, "input"), []byte("test\n"))
	ageWrite(t, filepath.Join(dir, "key.txt"), []byte(ageTestIdentity+"\n"))
	ageWrite(t, filepath.Join(dir, "nonexistent-identity.txt"), []byte("AGE-PLUGIN-NONEXISTENTPLUGIN-1R4XFW4\n"))
	ageWrite(t, filepath.Join(dir, "pwn-identity.txt"), []byte("AGE-PLUGIN-PWN/PWN-19GYK4WLY\n"))
	r1 := ageKey(t, keygen, filepath.Join(dir, "k1"))
	t.Run("round trips", func(t *testing.T) { agePluginRoundTrips(t, oracle, port, dir, keygen) })
	t.Run("release plugins", func(t *testing.T) { agePluginRoundTrips(t, release, port, dir, keygen) })
	t.Run("messages", func(t *testing.T) { agePluginMessages(t, release, port, dir, r1) })
	t.Run("tag messages", func(t *testing.T) { ageTagMessages(t, release, port, dir) })
	t.Run("terminal", func(t *testing.T) { agePluginTerminal(t, release, port, dir) })
	t.Run("programs", func(t *testing.T) { agePluginPrograms(t, dir, ours, orig) })
}

// agePluginPrograms runs each plugin of the port and the plugin of the same
// name from the release archive of age, with the same arguments.
func agePluginPrograms(t *testing.T, dir, ours, release string) {
	ageWrite(t, filepath.Join(dir, "exists"), nil)
	ageWrite(t, filepath.Join(dir, "two-pq"), []byte(ageRead(t, filepath.Join(dir, "pq"))+ageRead(t, filepath.Join(dir, "pq"))))
	cases := [][]string{
		{"pq", "-identity", "pq"},
		{"pq", "-identity", "two-pq"},
		{"pq", "-identity", "k1"},
		{"pq", "-identity", "missing"},
		{"pq", "-identity", "pq", "pq"},
		{"pq", "-identity", "-o", "exists", "pq"},
		{"pq", "-identity", "input"},
		{"pq", "-identity", "-"},
	}
	for _, name := range []string{"pq", "batchpass", "tag", "tagpq"} {
		cases = append(cases, []string{name}, []string{name, "--age-plugin=identity-v2"}, []string{name, "-x"}, []string{name, "-h"}, []string{name, "--age-plugin"})
	}
	reportLine := regexp.MustCompile(`age-plugin-pq: report unexpected or unhelpful errors at https://filippo.io/age/report\n`)
	run := func(path string, args []string) ([]byte, string, int) {
		cmd := exec.Command("age-plugin-"+args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "PATH="+path)
		cmd.Stdin = strings.NewReader("")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		lp, err := lookPathIn("age-plugin-"+args[0], path)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Path = lp
		_ = cmd.Run()
		return stdout.Bytes(), reportLine.ReplaceAllString(stderr.String(), ""), cmd.ProcessState.ExitCode()
	}
	for _, args := range cases {
		wantOut, want, wantCode := run(release, args)
		gotOut, got, gotCode := run(ours, args)
		if gotCode != wantCode || got != want || !bytes.Equal(gotOut, wantOut) {
			t.Errorf("age-plugin-%s: age exit %d %q %q; fern exit %d %q %q", strings.Join(args, " "), wantCode, want, wantOut, gotCode, got, gotOut)
		}
	}
}

// lookPathIn is exec.LookPath with the PATH path.
func lookPathIn(file, path string) (string, error) {
	for _, d := range filepath.SplitList(path) {
		p := filepath.Join(d, file)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not on the PATH", file)
}

// ageDecryptsWith checks that each tool decrypts sealed with the
// arguments to the plaintext.
func ageDecryptsWith(t *testing.T, oracle, port ageTool, dir, what string, sealed, plain []byte, args ...string) {
	t.Helper()
	for _, tool := range []ageTool{oracle, port} {
		if got, stderr, code := tool.run(t, dir, sealed, append([]string{"-d"}, args...)...); code != 0 || !bytes.Equal(got, plain) {
			t.Errorf("%s -d %v of %s: exit %d: %s", tool.name, args, what, code, stderr)
		}
	}
}

func agePluginRoundTrips(t *testing.T, oracle, port ageTool, dir, keygen string) {
	plain := ageRandom(t, 70000)
	os.Remove(filepath.Join(dir, "pq"))
	pq := ageHybridKey(t, keygen, filepath.Join(dir, "pq"))
	conv := exec.Command("age-plugin-pq", "-identity", "pq")
	conv.Dir = dir
	pid, err := conv.Output()
	if err != nil || !strings.HasPrefix(string(pid), "AGE-PLUGIN-PQ-1") {
		t.Fatalf("age-plugin-pq -identity: %v %q", err, pid)
	}
	ageWrite(t, filepath.Join(dir, "pid"), pid)
	for _, armor := range [][]string{nil, {"-a"}} {
		for _, c := range []struct {
			what string
			enc  []string
			dec  []string
			env  []string
		}{
			{"a P-256 tag recipient", []string{"-r", ageTagClassic}, []string{"-j", "tagtest"}, nil},
			{"a hybrid tag recipient", []string{"-r", ageTagHybrid}, []string{"-j", "tagtest"}, nil},
			{"two tag recipients", []string{"-r", ageTagHybrid, "-r", ageTagHybrid}, []string{"-j", "tagtest"}, nil},
			{"the test plugin", []string{"-r", ageTestRecipient}, []string{"-i", "key.txt"}, nil},
			{"the test plugin identity", []string{"-e", "-i", "key.txt"}, []string{"-i", "key.txt"}, nil},
			{"the pq plugin identity", []string{"-e", "-i", "pid"}, []string{"-i", "pid"}, nil},
			{"a hybrid recipient to the pq plugin identity", []string{"-r", pq}, []string{"-i", "pid"}, nil},
			{"batchpass", []string{"-e", "-j", "batchpass"}, []string{"-j", "batchpass"}, []string{"AGE_PASSPHRASE=pass word", "AGE_PASSPHRASE_WORK_FACTOR=5"}},
		} {
			ageEnv(t, c.env...)
			for _, tool := range []ageTool{oracle, port} {
				sealed, stderr, code := tool.run(t, dir, plain, append(append([]string{}, c.enc...), armor...)...)
				if code != 0 {
					t.Errorf("%s %v %v: exit %d: %s", tool.name, c.enc, armor, code, stderr)
					continue
				}
				ageDecryptsWith(t, oracle, port, dir, fmt.Sprintf("%s's file to %s %v", tool.name, c.what, armor), sealed, plain, c.dec...)
			}
		}
	}
	ageEnv(t)
	if got, _, code := oracle.run(t, dir, nil, "-d", "-i", "pq", "-o", "-", "pid"); code == 0 {
		t.Errorf("age -d of a plugin identity file: exit 0 %q", got)
	}
}

func agePluginMessages(t *testing.T, oracle, port ageTool, dir, r1 string) {
	sealed, _, _ := oracle.run(t, dir, []byte("test\n"), "-r", ageTestRecipient)
	ageWrite(t, filepath.Join(dir, "test.age"), sealed)
	ageEnv(t, "AGE_PASSPHRASE=password", "AGE_PASSPHRASE_WORK_FACTOR=5")
	sealed, _, _ = oracle.run(t, dir, []byte("test\n"), "-e", "-j", "batchpass")
	ageWrite(t, filepath.Join(dir, "batch.age"), sealed)
	sealed, _, _ = oracle.run(t, dir, []byte("test\n"), "-r", r1)
	ageWrite(t, filepath.Join(dir, "x.age"), sealed)
	type pcase struct {
		env  []string
		args []string
	}
	cases := []pcase{
		{nil, []string{"-r", "age1nonexistentplugin1pt5d8z", "-o", "t1.age", "input"}},
		{nil, []string{"-d", "-i", "nonexistent-identity.txt", "test.age"}},
		{nil, []string{"-d", "-j", "nonexistent", "test.age"}},
		{nil, []string{"-r", "age1pwn/pwn19gt89dfz", "input"}},
		{nil, []string{"-d", "-i", "pwn-identity.txt", "test.age"}},
		{nil, []string{"-d", "-j", "pwn/pwn", "test.age"}},
		{nil, []string{"-e", "-j", "", "input"}},
		{nil, []string{"-d", "-j", "TeSt", "test.age"}},
		{nil, []string{"-d", "-i", "key.txt", "x.age"}},
		{nil, []string{"-d", "-i", "k1", "-i", "key.txt", "test.age"}},
		{nil, []string{"-d", "-j", "test", "-i", "k1", "test.age"}},
		{nil, []string{"-r", ageTestRecipient, "-r", r1, "input"}},
		{[]string{"AGE_TEST_PLUGIN=labels"}, []string{"-r", ageTestRecipient, "-r", r1, "input"}},
		{[]string{"AGE_TEST_PLUGIN=labels"}, []string{"-r", r1, "-r", ageTestRecipient, "input"}},
		{nil, []string{"-r", ageTestRecipient, "-r", ageTagHybrid, "input"}},
		{[]string{"AGE_TEST_PLUGIN=msg"}, []string{"-r", ageTestRecipient, "-o", "m.age", "input"}},
		{[]string{"AGE_TEST_PLUGIN=msg"}, []string{"-d", "-i", "key.txt", "test.age"}},
		{[]string{"AGE_TEST_PLUGIN=two"}, []string{"-r", ageTestRecipient, "-o", "two.age", "input"}},
		{[]string{"AGE_TEST_PLUGIN=slow"}, []string{"-d", "-i", "key.txt", "test.age"}},
		{[]string{"AGE_PASSPHRASE=password", "AGE_PASSPHRASE_FD=3"}, []string{"-e", "-j", "batchpass", "-a", "input"}},
		{nil, []string{"-e", "-j", "batchpass", "-a", "input"}},
		{[]string{"AGE_PASSPHRASE=wrongpassword"}, []string{"-d", "-j", "batchpass", "batch.age"}},
		{[]string{"AGE_PASSPHRASE=password"}, []string{"-d", "-j", "batchpass", "batch.age"}},
		{[]string{"AGE_PASSPHRASE=password", "AGE_PASSPHRASE_MAX_WORK_FACTOR=3"}, []string{"-d", "-j", "batchpass", "batch.age"}},
		{[]string{"AGE_PASSPHRASE=password", "AGE_PASSPHRASE_MAX_WORK_FACTOR=31"}, []string{"-d", "-j", "batchpass", "batch.age"}},
		{[]string{"AGE_PASSPHRASE=password", "AGE_PASSPHRASE_WORK_FACTOR=x"}, []string{"-e", "-j", "batchpass", "input"}},
		{[]string{"AGE_PASSPHRASE=password", "AGE_PASSPHRASE_WORK_FACTOR=0"}, []string{"-e", "-j", "batchpass", "input"}},
		{[]string{"AGE_PASSPHRASE_FD=abc"}, []string{"-e", "-j", "batchpass", "input"}},
		{[]string{"AGE_PASSPHRASE_FD=-1"}, []string{"-e", "-j", "batchpass", "input"}},
		{[]string{"AGE_PASSPHRASE_FD=9"}, []string{"-e", "-j", "batchpass", "input"}},
		{[]string{"AGE_PASSPHRASE=password"}, []string{"-e", "-j", "batchpass", "-r", r1, "-a", "input"}},
		{[]string{"AGE_PASSPHRASE=password"}, []string{"-e", "-r", r1, "-j", "batchpass", "-a", "input"}},
		{[]string{"AGE_PASSPHRASE=password"}, []string{"-d", "-j", "batchpass", "test.age"}},
		{[]string{"AGE_PASSPHRASE=password"}, []string{"-d", "-j", "batchpass", "x.age"}},
		{nil, []string{"-d", "-j", "tagtest", "test.age"}},
		{nil, []string{"-e", "-j", "tagtest", "input"}},
		{nil, []string{"-e", "-j", "tag", "input"}},
		{nil, []string{"-d", "-j", "pq", "test.age"}},
	}
	for _, mode := range []string{"error", "zero", "index", "short", "dupkey", "badconfirm", "crash"} {
		cases = append(cases,
			pcase{[]string{"AGE_TEST_PLUGIN=" + mode}, []string{"-r", ageTestRecipient, "input"}},
			pcase{[]string{"AGE_TEST_PLUGIN=" + mode}, []string{"-d", "-i", "key.txt", "test.age"}})
	}
	for _, c := range cases {
		ageEnv(t, c.env...)
		wantOut, want, wantCode := oracle.run(t, dir, nil, c.args...)
		gotOut, got, gotCode := port.run(t, dir, nil, c.args...)
		want, got = ageScryptLabel.ReplaceAllString(want, `"LABEL"`), ageScryptLabel.ReplaceAllString(got, `"LABEL"`)
		if gotCode != wantCode || got != want || !bytes.Equal(gotOut, wantOut) && wantCode != 0 {
			t.Errorf("%v age %s: age exit %d %q; fern age exit %d %q", c.env, strings.Join(c.args, " "), wantCode, want, gotCode, got)
		}
	}
	ageEnv(t)
	for _, f := range []string{"t1.age"} {
		if ageExists(filepath.Join(dir, f)) {
			t.Errorf("a failed encryption left %s", f)
		}
	}
}

// ageTagMessages gives both tools malformed tag recipients and malformed
// tag stanzas.
func ageTagMessages(t *testing.T, oracle, port ageTool, dir string) {
	point := make([]byte, 33)
	point[0] = 2
	noRoot := append([]byte{}, point...)
	noRoot[32] = 1
	bigX := append([]byte{2}, bytes.Repeat([]byte{0xff}, 32)...)
	prefix4 := append([]byte{4}, point[1:]...)
	recipients := []string{
		ageBech32("age1tag", point),
		ageBech32("age1tag", noRoot),
		ageBech32("age1tag", bigX),
		ageBech32("age1tag", prefix4),
		ageBech32("age1tag", point[:32]),
		ageBech32("age1tagpq", point),
		ageBech32("age1tagpq", make([]byte, 1249)),
		ageTagClassic[:len(ageTagClassic)-1],
		strings.ToUpper(ageTagClassic),
		"age1tag1",
		ageTagHybrid[:len(ageTagHybrid)-1] + "q",
	}
	var cases [][]string
	for _, r := range recipients {
		cases = append(cases, []string{"-r", r, "input"})
	}
	ageWrite(t, filepath.Join(dir, "tag-rf"), []byte(ageTagClassic+"\n"+ageTagHybrid+"\n"))
	cases = append(cases, []string{"-R", "tag-rf", "input"})
	for _, hybrid := range []bool{false, true} {
		r, kind, size := ageTagClassic, "p256tag", 65
		if hybrid {
			r, kind, size = ageTagHybrid, "mlkem768p256tag", 1153
		}
		sealed, _, _ := oracle.run(t, dir, []byte("hello"), "-r", r)
		lines := strings.Split(string(sealed), "\n")
		args := strings.Fields(strings.TrimPrefix(lines[1], "-> "+kind+" "))
		tag, enc := args[0], args[1]
		raw, err := base64.RawStdEncoding.DecodeString(enc)
		if err != nil || len(raw) != size {
			t.Fatalf("age's %s stanza: %v, %d bytes", kind, err, len(raw))
		}
		b64 := base64.RawStdEncoding.EncodeToString
		edit := func(old, new string) []byte { return []byte(strings.Replace(string(sealed), old, new, 1)) }
		offCurve := append([]byte{}, raw...)
		offCurve[size-1] ^= 1
		files := map[string][]byte{
			"ok":        sealed,
			"tag-flip":  edit(tag, b64([]byte{1, 2, 3, 4})),
			"tag-short": edit(tag, b64([]byte{1, 2, 3})),
			"tag-bad":   edit(tag, tag+"="),
			"enc-short": edit(enc, b64(raw[:size-1])),
			"enc-bad":   edit(enc, "!"+enc[1:]),
			"enc-curve": edit(enc, b64(offCurve)),
			"one-arg":   edit(" "+enc, ""),
			"long-body": edit("\n"+lines[2]+"\n", "\n"+b64(make([]byte, 33))+"\n"),
		}
		for name, data := range files {
			file := kind + "-" + name
			ageWrite(t, filepath.Join(dir, file), data)
			cases = append(cases, []string{"-d", "-j", "tagtest", file})
		}
	}
	for _, args := range cases {
		wantOut, want, wantCode := oracle.run(t, dir, nil, args...)
		gotOut, got, gotCode := port.run(t, dir, nil, args...)
		if gotCode != wantCode || got != want || wantCode != 0 && !bytes.Equal(gotOut, wantOut) || args[0] == "-d" && !bytes.Equal(gotOut, wantOut) {
			t.Errorf("age %s: age exit %d %q; fern age exit %d %q", strings.Join(args, " "), wantCode, want, gotCode, got)
		}
	}
}

// agePluginPrompt is the end of each prompt of the test plugin.
var agePluginPrompt = regexp.MustCompile(`(?:value:|"\)) `)

// agePluginTerminal checks the questions of a plugin through the terminal:
// both tools show the same text and give the plugin the same answers.
func agePluginTerminal(t *testing.T, oracle, port ageTool, dir string) {
	for _, c := range []struct {
		mode  string
		lines []string
	}{
		{"secret", []string{"sesame\n"}},
		{"secret", []string{"wrong\n"}},
		{"public", []string{"sesame\n"}},
		{"public", []string{"sesam\x7fme\n"}},
		{"confirm", []string{"1"}},
		{"confirm", []string{"2"}},
		{"confirm", []string{"3", "1"}},
		{"enter", []string{"\n"}},
	} {
		ageEnv(t, "AGE_TEST_PLUGIN="+c.mode)
		for _, args := range [][]string{{"-r", ageTestRecipient, "-o", "tty.age", "input"}, {"-d", "-i", "key.txt", "test.age"}} {
			wantOut, want, wantScreen, wantCode := oracle.runTTYPrompt(t, dir, nil, agePluginPrompt, c.lines, args...)
			gotOut, got, screen, gotCode := port.runTTYPrompt(t, dir, nil, agePluginPrompt, c.lines, args...)
			if gotCode != wantCode || got != want || screen != wantScreen || !bytes.Equal(gotOut, wantOut) {
				t.Errorf("%s %q age %v: age exit %d %q %q; fern age exit %d %q %q", c.mode, c.lines, args, wantCode, want, wantScreen, gotCode, got, screen)
			}
		}
	}
	ageEnv(t)
}
