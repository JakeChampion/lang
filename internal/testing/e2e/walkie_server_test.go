package e2e

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// writeTestIdentity writes a self-signed P-256 certificate for 127.0.0.1 and
// its PKCS #8 key, and gives the pool that trusts the certificate.
func writeTestIdentity(t *testing.T, dir string) (string, string, *x509.CertPool) {
	t.Helper()
	return writeTestIdentityKey(t, dir, false)
}

// writeTestIdentityKey writes the key in the SEC1 form, the form that Caddy
// writes, when sec1 is true.
func writeTestIdentityKey(t *testing.T, dir string, sec1 bool) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "walkie test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyType := "PRIVATE KEY"
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if sec1 {
		keyType = "EC PRIVATE KEY"
		keyDER, err = x509.MarshalECPrivateKey(key)
	}
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: keyType, Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return certPath, keyPath, pool
}

// freePort gives a port that is free for TCP and for UDP on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	for k := 0; k < 20; k++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		u, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
		l.Close()
		if err == nil {
			u.Close()
			return port
		}
	}
	t.Fatal("no port is free for TCP and UDP")
	return 0
}

// ask sends one request line over TLS and gives the reply line.
func ask(t *testing.T, addr string, pool *x509.CertPool, request string) string {
	t.Helper()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr,
		&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		t.Fatalf("write %q: %v", request, err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reply to %q: %v (got %q)", request, err, line)
	}
	return strings.TrimSpace(line)
}

func tokenOf(t *testing.T, line string) []byte {
	t.Helper()
	words := strings.Fields(line)
	b, err := hex.DecodeString(words[len(words)-1])
	if err != nil || len(b) != 16 {
		t.Fatalf("no token in %q", line)
	}
	return b
}

// memberID is the ID of the member with token, as rooms.member_id gives it.
func memberID(token []byte) []byte {
	h := sha256.Sum256(append([]byte("walkie relay id"), token...))
	return h[:8]
}

// sealed is kind, the ID of the member, the counter and body, with the MAC
// that the token keys, as rooms.wrap and rooms.answer_challenge make it.
func sealed(kind byte, token []byte, counter uint64, body []byte) []byte {
	out := append([]byte{kind}, memberID(token)...)
	out = binary.BigEndian.AppendUint64(out, counter)
	out = append(out, body...)
	m := hmac.New(sha256.New, token)
	m.Write(out)
	return append(out, m.Sum(nil)[:16]...)
}

// member is one phone of a room on the relay, with its counter.
type member struct {
	token   []byte
	counter uint64
}

func (m *member) datagram(body string) []byte {
	m.counter++
	return sealed(248, m.token, m.counter, []byte(body))
}

// arrive sends a datagram of m from c and answers the challenge of the
// relay, so the relay sends the datagrams of the room to c.
func arrive(t *testing.T, c *net.UDPConn, relay *net.UDPAddr, m *member) {
	t.Helper()
	for k := 0; k < 20; k++ {
		if _, err := c.WriteToUDP(m.datagram("hello"), relay); err != nil {
			t.Fatal(err)
		}
		got := receive(c, 250*time.Millisecond)
		if len(got) == 25 && got[0] == 249 && got[1:9] == string(memberID(m.token)) {
			m.counter++
			if _, err := c.WriteToUDP(sealed(250, m.token, m.counter, []byte(got[9:])), relay); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("the relay sent no challenge")
}

// receive gives the next datagram on c, or "" when none comes in time.
func receive(c *net.UDPConn, wait time.Duration) string {
	buf := make([]byte, 2048)
	c.SetReadDeadline(time.Now().Add(wait))
	n, err := c.Read(buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

// send writes one datagram to the relay, again until the peer has it: the
// first datagram of a member can arrive before the relay knows the peer.
func sendUntil(t *testing.T, from *net.UDPConn, relay *net.UDPAddr, m *member, to *net.UDPConn, want string) {
	t.Helper()
	for k := 0; k < 20; k++ {
		if _, err := from.WriteToUDP(m.datagram(want), relay); err != nil {
			t.Fatal(err)
		}
		if got := receive(to, 250*time.Millisecond); got == want {
			return
		}
	}
	t.Fatalf("the relay did not deliver %q", want)
}

func udpConn(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// The server of the internet mode on the host: two phones make and join a
// room over TLS, and the relay sends their datagrams to each other. A phone
// that moves to a new address keeps its room.
func TestWalkieRelayServer(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "server")
	build := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(walkieApp, "server.fern"), e2eharness.SelfHostStdlibRoot(t))
	build.Env = e2eharness.ChildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server.fern: %v\n%s", err, out)
	}
	certPath, keyPath, pool := writeTestIdentity(t, dir)
	port := freePort(t)
	var logs bytes.Buffer
	srv := exec.Command(bin, certPath, keyPath, strconv.Itoa(port))
	srv.Stdout = &logs
	srv.Stderr = &logs
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Process.Kill()
		srv.Wait()
		if t.Failed() {
			t.Logf("server log:\n%s", logs.String())
		}
	})
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for k := 0; ; k++ {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			break
		}
		if k == 100 {
			t.Fatalf("the server does not listen: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	created := ask(t, addr, pool, "CREATE")
	words := strings.Fields(created)
	if len(words) != 3 || words[0] != "ROOM" || len(words[1]) != 8 {
		t.Fatalf("CREATE: %q", created)
	}
	joined := ask(t, addr, pool, "JOIN "+strings.ToLower(words[1]))
	if !strings.HasPrefix(joined, "JOINED ") {
		t.Fatalf("JOIN: %q", joined)
	}
	if full := ask(t, addr, pool, "JOIN "+words[1]); full != "ERROR the room is full" {
		t.Fatalf("a third JOIN: %q", full)
	}
	if bad := ask(t, addr, pool, "HELLO"); bad != "ERROR unknown request" {
		t.Fatalf("HELLO: %q", bad)
	}

	// The request of the app, which steps with no wait, gets a room too, and
	// it refuses a server whose certificate it does not trust.
	client := filepath.Join(dir, "room_client")
	buildClient := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", client,
		e2eharness.RepoPath(walkieApp, "room_client.fern"), e2eharness.SelfHostStdlibRoot(t))
	buildClient.Env = e2eharness.ChildEnv()
	if out, err := buildClient.CombinedOutput(); err != nil {
		t.Fatalf("build room_client.fern: %v\n%s", err, out)
	}
	out, err := exec.Command(client, "127.0.0.1", strconv.Itoa(port), certPath, "CREATE").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "ROOM ") || !strings.Contains(string(out), "relay 127.0.0.1:"+strconv.Itoa(port)) {
		t.Fatalf("room_client CREATE: %v\n%s", err, out)
	}
	otherCertPath, _, _ := writeTestIdentity(t, t.TempDir())
	out, err = exec.Command(client, "127.0.0.1", strconv.Itoa(port), otherCertPath, "CREATE").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "ERROR ") {
		t.Fatalf("room_client trusted a certificate that it did not know: %v\n%s", err, out)
	}
	ma, mb := &member{token: tokenOf(t, created)}, &member{token: tokenOf(t, joined)}

	relay := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	a, b := udpConn(t), udpConn(t)
	arrive(t, a, relay, ma)
	arrive(t, b, relay, mb)
	sendUntil(t, b, relay, mb, a, "from b")
	sendUntil(t, a, relay, ma, b, "from a")

	// A copy of a datagram of b from another address moves nothing.
	copied := udpConn(t)
	dup := mb.datagram("copied")
	if _, err := b.WriteToUDP(dup, relay); err != nil {
		t.Fatal(err)
	}
	if got := receive(a, 300*time.Millisecond); got != "copied" {
		t.Fatalf("the relay did not deliver the datagram of b: %q", got)
	}
	copied.WriteToUDP(dup, relay)
	if got := receive(copied, 300*time.Millisecond); got != "" {
		t.Fatalf("the relay answered a copied datagram: %q", got)
	}
	sendUntil(t, a, relay, ma, b, "still to b")

	moved := udpConn(t)
	arrive(t, moved, relay, ma)
	sendUntil(t, moved, relay, ma, b, "a moved")
	sendUntil(t, b, relay, mb, moved, "to the new address")

	stranger := udpConn(t)
	stranger.WriteToUDP(sealed(248, make([]byte, 16), 1, []byte("not a member")), relay)
	if got := receive(b, 300*time.Millisecond); got != "" {
		t.Fatalf("the relay sent a datagram with an unknown token: %q", got)
	}

	// A renewal replaces the files, as Caddy does. A new certificate with the
	// old key does not load, so the old identity stays. When the new key
	// arrives too, new clients get the new certificate with no restart.
	renewal := t.TempDir()
	newCert, newKey, newPool := writeTestIdentityKey(t, renewal, true)
	if err := os.Rename(newCert, certPath); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, addr, pool, "CREATE"); !strings.HasPrefix(got, "ROOM ") {
		t.Fatalf("CREATE with half a renewal: %q", got)
	}
	if err := os.Rename(newKey, keyPath); err != nil {
		t.Fatal(err)
	}
	if got := ask(t, addr, newPool, "CREATE"); !strings.HasPrefix(got, "ROOM ") {
		t.Fatalf("CREATE after the renewal: %q", got)
	}
	old, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr,
		&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13})
	if err == nil {
		old.Close()
		t.Fatal("the old certificate still verifies after the renewal")
	}
}
