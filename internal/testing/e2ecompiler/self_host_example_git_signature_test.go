package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitSignedRepo runs git in one repository with fixed persons and dates.
// Each call moves the dates one hour on.
type gitSignedRepo struct {
	t      *testing.T
	oracle gitTool
	dir    string
	when   int
	env    []string
}

func (g *gitSignedRepo) run(args ...string) string {
	g.t.Helper()
	g.when += 3600
	cmd := g.oracle.cmd(args...)
	cmd.Dir = g.dir
	stamp := fmt.Sprintf("%d +0000", g.when)
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
		"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL=c@example.com",
		"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp), g.env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// tamper writes a copy of the commit HEAD with the message `from` changed
// to `to`, which breaks its signature, and points the branch at the copy.
func (g *gitSignedRepo) tamper(from, to string) {
	g.t.Helper()
	body := strings.Replace(g.run("cat-file", "commit", "HEAD")+"\n", "\n"+from+"\n", "\n"+to+"\n", 1)
	file := filepath.Join(g.dir, ".git", "tampered")
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		g.t.Fatal(err)
	}
	g.run("update-ref", "HEAD", g.run("hash-object", "-t", "commit", "-w", file))
}

// gitSignatureFormCases are the forms of log with signatures, for each
// format of a signature.
var gitSignatureFormCases = []string{
	"log --show-signature", "log --show-signature --oneline", "log --show-signature --graph --color", "log --show-signature --color",
	"log --format=%h|%G?|%GS|%GK|%GF|%GP|%GT", "log --format=%h%n%GG", "log --show-signature --format=%h --all",
	"log --show-signature --pretty=raw -1", "log --show-signature --reverse --oneline", "log --format=%G? --all",
	"log --show-signature --format=%h|%G?", "log --show-signature -g -3", "log --show-signature -g --oneline -3",
	"log --show-signature -m --oneline -1",
}

// gitSSHSignedFixture makes three keys of ssh-keygen and a repository with
// an unsigned commit, the signature of a key of the allowed signers, of a
// key that no principal has, of a revoked key, of a key that is valid only
// before the date of its commit, and a bad signature. The allowed signers and the revoked keys are files of the
// git directory, which the configuration names from the top of the
// worktree. A missing ssh-keygen fails the test.
func gitSSHSignedFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) {
	t.Helper()
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatal("ssh-keygen is required for the ssh signatures of the git example")
	}
	keys := t.TempDir()
	public := map[string]string{}
	for _, name := range []string{"good", "other", "revoked", "old"} {
		path := filepath.Join(keys, name)
		if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", name, "-f", path).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
		text, err := os.ReadFile(path + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(text))
		public[name] = fields[0] + " " + fields[1]
	}
	g := &gitSignedRepo{t: t, oracle: oracle, dir: dir, when: 1700000000}
	key := func(name string) []string {
		return []string{"-c", "gpg.format=ssh", "-c", "user.signingkey=" + filepath.Join(keys, name+".pub")}
	}
	g.run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	allowed := "s@example.com " + public["good"] + "\nr@example.com " + public["revoked"] + "\nold@example.com valid-before=\"20200101\" " + public["old"] + "\n"
	for name, text := range map[string]string{"allowed_signers": allowed, "revoked_keys": public["revoked"] + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".git", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "f"), []byte("f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g.run("add", "d/f")
	g.run("commit", "-q", "-m", "unsigned")
	for _, name := range []string{"good", "other", "revoked", "old"} {
		g.run(append(key(name), "commit", "-q", "--allow-empty", "-S", "-m", "signed by "+name)...)
	}
	g.run(append(key("good"), "commit", "-q", "--allow-empty", "-S", "-m", "to break")...)
	g.tamper("to break", "broken")
	g.run("config", "gpg.ssh.allowedSignersFile", ".git/allowed_signers")
}

// gitX509SignedFixture makes a certificate of gpgsm and a repository with
// an unsigned commit, a good signature and a bad signature. It gives the directory of gpgsm. A missing gpgsm fails
// the test.
func gitX509SignedFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) string {
	t.Helper()
	gpgsm, err := exec.LookPath("gpgsm")
	if err != nil {
		t.Fatal("gpgsm is required for the x509 signatures of the git example")
	}
	// The socket of gpg-agent needs a short path.
	home, err := os.MkdirTemp("", "gsm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec.Command("gpgconf", "--homedir", home, "--kill", "gpg-agent").Run()
		os.RemoveAll(home)
	})
	if err := os.WriteFile(filepath.Join(home, "gpgsm.conf"), []byte("disable-crl-checks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params := "Key-Type: RSA\nKey-Length: 2048\nKey-Usage: sign\nSerial: random\nName-DN: CN=Signer,O=Example\nName-Email: s@example.com\n"
	if err := os.WriteFile(filepath.Join(home, "params"), []byte(params), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gpgsm, "--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase", "", "--generate-key", filepath.Join(home, "params"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cert, err := cmd.Output()
	if err != nil {
		t.Fatalf("gpgsm --generate-key: %v\n%s", err, stderr.String())
	}
	cmd = exec.Command(gpgsm, "--homedir", home, "--batch", "--import")
	cmd.Stdin = strings.NewReader(string(cert))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gpgsm --import: %v\n%s", err, out)
	}
	list, err := exec.Command(gpgsm, "--homedir", home, "-K", "--with-colons").Output()
	if err != nil {
		t.Fatalf("gpgsm -K: %v", err)
	}
	var trust strings.Builder
	for _, line := range strings.Split(string(list), "\n") {
		if f := strings.Split(line, ":"); f[0] == "fpr" && len(f) > 9 {
			trust.WriteString(f[9] + " S relax\n")
		}
	}
	if err := os.WriteFile(filepath.Join(home, "trustlist.txt"), []byte(trust.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("gpgconf", "--homedir", home, "--kill", "gpg-agent").Run()
	g := &gitSignedRepo{t: t, oracle: oracle, dir: dir, when: 1700000000, env: []string{"GNUPGHOME=" + home}}
	key := []string{"-c", "gpg.format=x509", "-c", "user.signingkey=s@example.com"}
	g.run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	g.run("commit", "-q", "--allow-empty", "-m", "unsigned")
	g.run(append(key, "commit", "-q", "--allow-empty", "-S", "-m", "signed")...)
	g.run(append(key, "commit", "-q", "--allow-empty", "-S", "-m", "to break")...)
	g.tamper("to break", "broken")
	return home
}

// gitGPGSignedMergesFixture makes a repository with the signature of a key
// that expired before the check, and a commit whose signature has no known format. It gives the directory of
// the keyring.
func gitGPGSignedMergesFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) string {
	t.Helper()
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Fatal("gpg is required for the signatures of the git example")
	}
	var homes []string
	for range 3 {
		home, err := os.MkdirTemp("", "gpg")
		if err != nil {
			t.Fatal(err)
		}
		homes = append(homes, home)
		t.Cleanup(func() {
			exec.Command("gpgconf", "--homedir", home, "--kill", "gpg-agent").Run()
			os.RemoveAll(home)
		})
	}
	// The third key expires one day after a time in 2020, at which gpg makes
	// it and signs with it.
	if err := os.WriteFile(filepath.Join(homes[2], "gpg.conf"), []byte("faked-system-time 1600000000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i, who := range []string{"Signer <s@example.com>", "Stranger <x@example.com>", "Old <o@example.com>"} {
		expiry := []string{"never", "never", "1d"}[i]
		cmd := exec.Command(gpg, "--homedir", homes[i], "--batch", "--pinentry-mode", "loopback", "--passphrase", "", "--quick-gen-key", who, "ed25519", "sign", expiry)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gpg --quick-gen-key: %v\n%s", err, out)
		}
	}
	g := &gitSignedRepo{t: t, oracle: oracle, dir: dir, when: 1700000000, env: []string{"GNUPGHOME=" + homes[0]}}
	g.run("init", "-q", "-b", "main", "--object-format="+hash.format, ".")
	g.run("commit", "-q", "--allow-empty", "-m", "unsigned")
	g.env = []string{"GNUPGHOME=" + homes[2]}
	g.run("commit", "-q", "--allow-empty", "-So@example.com", "-m", "expired key")
	g.env = []string{"GNUPGHOME=" + homes[0]}
	if err := os.Remove(filepath.Join(homes[2], "gpg.conf")); err != nil {
		t.Fatal(err)
	}
	exported, err := exec.Command(gpg, "--homedir", homes[2], "--export", "o@example.com").Output()
	if err != nil {
		t.Fatalf("gpg --export: %v", err)
	}
	cmd := exec.Command(gpg, "--homedir", homes[0], "--batch", "--import")
	cmd.Stdin = strings.NewReader(string(exported))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gpg --import: %v\n%s", err, out)
	}
	// A signature of no known format stops git.
	body := strings.Replace(g.run("cat-file", "commit", "HEAD")+"\n", "\nexpired key\n", "\nno format\n", 1)
	body = strings.Replace(body, "gpgsig -----BEGIN PGP SIGNATURE-----", "gpgsig -----BEGIN NO SIGNATURE-----", 1)
	if hash.format == "sha256" {
		body = strings.Replace(body, "gpgsig-sha256 -----BEGIN PGP SIGNATURE-----", "gpgsig-sha256 -----BEGIN NO SIGNATURE-----", 1)
	}
	file := filepath.Join(dir, ".git", "noformat")
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	g.run("update-ref", "refs/heads/noformat", g.run("hash-object", "-t", "commit", "-w", "--literally", file))
	// gpg checks the trust database once, with a message of its own.
	exec.Command(gpg, "--homedir", homes[0], "--check-trustdb").Run()
	return homes[0]
}

// TestSelfHostExampleGitSignatures runs log with the signatures of ssh, of
// x509 and of gpg with git and with the port: the settings of ssh-keygen,
// an expired key, and a signature of no known format.
func TestSelfHostExampleGitSignatures(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			env := []string{"TZ=UTC"}
			config := func(dir string, args ...string) {
				t.Helper()
				if _, stderr, code := oracle.run(t, dir, "", append([]string{"config"}, args...)...); code != 0 {
					t.Fatalf("git config %v: %s", args, stderr)
				}
			}
			ssh := t.TempDir()
			gitSSHSignedFixture(t, oracle, ssh, run)
			for _, at := range []string{ssh, filepath.Join(ssh, "d")} {
				for _, c := range gitSignatureFormCases {
					gitCompareEnv(t, oracle, port, at, env, strings.Fields(c)...)
				}
			}
			// A program that exits before it reads its input can stop git with
			// SIGPIPE, so no setting names such a program.
			settings := [][]string{{"gpg.ssh.revocationFile", ".git/revoked_keys"}, {"gpg.ssh.revocationFile", ".git/nosuch"},
				{"--unset", "gpg.ssh.revocationFile"}, {"gpg.minTrustLevel", "ultimate"}, {"gpg.minTrustLevel", "Fully"},
				{"--unset", "gpg.minTrustLevel"}, {"gpg.ssh.program", "nosuch"}, {"--unset", "gpg.ssh.program"},
				{"gpg.ssh.allowedSignersFile", ".git/nosuch"}, {"--unset", "gpg.ssh.allowedSignersFile"}}
			for _, s := range settings {
				config(ssh, s...)
				for _, at := range []string{ssh, filepath.Join(ssh, "d")} {
					for _, c := range []string{"log --show-signature --color", "log --format=%h|%G?|%GS|%GK|%GT"} {
						gitCompareEnv(t, oracle, port, at, env, strings.Fields(c)...)
					}
				}
			}
			x509 := t.TempDir()
			home := gitX509SignedFixture(t, oracle, x509, run)
			for _, c := range gitSignatureFormCases {
				gitCompareEnv(t, oracle, port, x509, append(env, "GNUPGHOME="+home), strings.Fields(c)...)
			}
			config(x509, "gpg.x509.program", "nosuch")
			gitCompareEnv(t, oracle, port, x509, append(env, "GNUPGHOME="+home), "log", "--show-signature", "--format=%h|%G?")
			gpg := t.TempDir()
			keys := gitGPGSignedMergesFixture(t, oracle, gpg, run)
			for _, c := range gitSignatureFormCases {
				gitCompareEnv(t, oracle, port, gpg, append(env, "GNUPGHOME="+keys), strings.Fields(c)...)
			}
			for _, c := range []string{"log --show-signature noformat", "log --format=%h%G? noformat", "log --show-signature --reverse --oneline noformat",
				"log --show-signature -g --oneline noformat", "log --show-signature -g noformat"} {
				gitCompareEnv(t, oracle, port, gpg, append(env, "GNUPGHOME="+keys), strings.Fields(c)...)
			}
		})
	}
}
