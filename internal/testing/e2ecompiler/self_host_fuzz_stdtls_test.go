package e2ecompiler

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The targets here feed hostile bytes to the TLS readers of the standard
// library through tests/fuzz/stdparse.fern: the record deframer and the
// handshake message reader, a server and a client handshake, and the PEM and
// X.509 readers with chain verification. The seeds come from Go's crypto/tls
// and crypto/x509, so the Fern readers start from bytes another
// implementation wrote.

// stdparseVerifyTime is the time the x509 reader verifies at, 2027-01-15.
const stdparseVerifyTime = 1800000000

func goCertificate(f testing.TB, key crypto.Signer, names []string, notAfter time.Time) []byte {
	f.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "example.com"},
		NotBefore:             time.Unix(stdparseVerifyTime, 0).Add(-365 * 24 * time.Hour),
		NotAfter:              notAfter,
		DNSNames:              names,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		f.Fatal(err)
	}
	return der
}

// goCertificates gives one self-signed certificate for example.com per key
// type the verifier accepts: ECDSA P-256, Ed25519 and RSA 2048.
func goCertificates(f testing.TB) [][]byte {
	f.Helper()
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		f.Fatal(err)
	}
	valid := time.Unix(stdparseVerifyTime, 0).Add(365 * 24 * time.Hour)
	var out [][]byte
	for _, k := range []crypto.Signer{ec, ed, rs} {
		out = append(out, goCertificate(f, k, []string{"example.com"}, valid))
	}
	return out
}

func pemBundle(ders [][]byte) []byte {
	var b bytes.Buffer
	for _, d := range ders {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: d})
	}
	return b.Bytes()
}

// recordingConn keeps every write in order, with the side that wrote it.
type recordingConn struct {
	net.Conn
	side   string
	mu     *sync.Mutex
	writes *[]sideWrite
}

type sideWrite struct {
	side string
	data []byte
}

func (c recordingConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	*c.writes = append(*c.writes, sideWrite{c.side, append([]byte(nil), b...)})
	c.mu.Unlock()
	return c.Conn.Write(b)
}

// goHandshake runs a Go client against a Go server over loopback TCP, and
// gives the client's first flight and the server's reply to it. A pipe would
// deadlock: after a HelloRetryRequest both sides write before either reads.
func goHandshake(f testing.TB, server *tls.Config, client *tls.Config) (hello, reply []byte) {
	f.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		f.Fatal(err)
	}
	defer ln.Close()
	cp, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		f.Fatal(err)
	}
	sp, err := ln.Accept()
	if err != nil {
		f.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	cp.SetDeadline(deadline)
	sp.SetDeadline(deadline)
	var mu sync.Mutex
	var writes []sideWrite
	cc := tls.Client(recordingConn{cp, "client", &mu, &writes}, client)
	sc := tls.Server(recordingConn{sp, "server", &mu, &writes}, server)
	done := make(chan error, 1)
	go func() { done <- sc.Handshake() }()
	if err := cc.Handshake(); err != nil {
		f.Fatal(err)
	}
	if err := <-done; err != nil {
		f.Fatal(err)
	}
	cp.Close()
	sp.Close()
	clientWrites := 0
	for _, w := range writes {
		switch {
		case w.side == "client":
			clientWrites++
			if clientWrites == 1 {
				hello = append(hello, w.data...)
			}
		case clientWrites == 1:
			reply = append(reply, w.data...)
		}
	}
	return hello, reply
}

// fernClientHello gives the ClientHello of the client that the tls-client
// reader starts.
func fernClientHello(f testing.TB, bin string) []byte {
	f.Helper()
	path := filepath.Join(f.TempDir(), "empty")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		f.Fatal(err)
	}
	out, err := exec.Command(bin, "tls-hello", path).Output()
	if err != nil {
		f.Fatal(err)
	}
	text := strings.TrimSpace(string(out))
	if !strings.HasPrefix(text, "tls-hello ") {
		f.Fatalf("stdparse tls-hello gives %q", text)
	}
	return hexSeed(f, strings.TrimPrefix(text, "tls-hello "))
}

// goReplyTo sends a ClientHello to a Go server and gives every byte the
// server writes until it waits for the client's next flight.
func goReplyTo(f testing.TB, server *tls.Config, hello []byte) []byte {
	f.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		f.Fatal(err)
	}
	defer ln.Close()
	go func() {
		sp, err := ln.Accept()
		if err != nil {
			return
		}
		defer sp.Close()
		sp.SetDeadline(time.Now().Add(30 * time.Second))
		_ = tls.Server(sp, server).Handshake()
	}()
	cp, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		f.Fatal(err)
	}
	defer cp.Close()
	if _, err := cp.Write(hello); err != nil {
		f.Fatal(err)
	}
	var reply []byte
	buf := make([]byte, 1<<16)
	for {
		cp.SetReadDeadline(time.Now().Add(time.Second))
		n, err := cp.Read(buf)
		reply = append(reply, buf[:n]...)
		if err != nil {
			break
		}
	}
	if len(reply) == 0 {
		f.Fatal("the Go server wrote no reply")
	}
	return reply
}

func goServerConfig(f testing.TB) *tls.Config {
	f.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	der := goCertificate(f, key, []string{"example.com"}, time.Now().Add(time.Hour))
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13}
}

func goClientConfig() *tls.Config {
	return &tls.Config{ServerName: "example.com", InsecureSkipVerify: true, MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}
}

// tlsRecord frames one TLS record.
func tlsRecord(kind byte, payload []byte) []byte {
	out := []byte{kind, 3, 3, 0, 0}
	binary.BigEndian.PutUint16(out[3:], uint16(len(payload)))
	return append(out, payload...)
}

// hsMessage frames one handshake message.
func hsMessage(kind byte, body []byte) []byte {
	n := len(body)
	return append([]byte{kind, byte(n >> 16), byte(n >> 8), byte(n)}, body...)
}

// FuzzSelfHostStdTLS feeds hostile bytes to the TLS record deframer, and the
// handshake records to the message reader and the extension readers.
func FuzzSelfHostStdTLS(f *testing.F) {
	fuzzStdReader(f)
}

func tlsSeeds(f testing.TB) [][]byte {
	hello, reply := goHandshake(f, goServerConfig(f), goClientConfig())
	return [][]byte{
		hello,
		reply,
		hello[5:],
		tlsRecord(21, []byte{2, 40}),
		tlsRecord(20, []byte{1}),
		tlsRecord(22, hsMessage(1, nil)),
		tlsRecord(22, []byte{1, 0xff, 0xff, 0xff}),
		tlsRecord(22, hsMessage(24, []byte{1})),
		tlsRecord(22, hsMessage(20, bytes.Repeat([]byte{7}, 32))),
		append(tlsRecord(22, hello[5:9]), tlsRecord(22, hello[9:])...),
		[]byte{22, 3, 3, 0x40, 0x01},
		[]byte{22, 3, 3, 0xff, 0xff, 1},
		[]byte{99, 3, 3, 0, 0},
		{},
	}
}

// FuzzSelfHostStdTLSServer feeds hostile bytes to a server handshake in pieces
// of eleven, starting from a ClientHello that Go's client wrote.
func FuzzSelfHostStdTLSServer(f *testing.F) {
	fuzzStdReader(f)
}

func tlsServerSeeds(f testing.TB) [][]byte {
	hello, _ := goHandshake(f, goServerConfig(f), goClientConfig())
	tls12 := &tls.Config{ServerName: "example.com", InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12}
	srv12 := goServerConfig(f)
	srv12.MinVersion = tls.VersionTLS12
	hello12, _ := goHandshake(f, srv12, tls12)
	return [][]byte{
		hello,
		hello12,
		append(tlsRecord(20, []byte{1}), hello...),
		append(append([]byte(nil), hello...), hello...),
		hello[:len(hello)/2],
		tlsRecord(21, []byte{2, 40}),
		tlsRecord(23, bytes.Repeat([]byte{0}, 40)),
		tlsRecord(22, hsMessage(1, bytes.Repeat([]byte{0xff}, 60))),
		{},
	}
}

// FuzzSelfHostStdTLSClient feeds hostile bytes to a client handshake as the
// server's flight, starting from the reply of Go's server to the client's own
// ClientHello.
func FuzzSelfHostStdTLSClient(f *testing.F) {
	fuzzStdReader(f)
}

func tlsClientSeeds(f testing.TB) [][]byte {
	hello := fernClientHello(f, fuzzStdParse(f))
	reply := goReplyTo(f, goServerConfig(f), hello)
	retry := goServerConfig(f)
	retry.CurvePreferences = []tls.CurveID{tls.CurveP256}
	retried := goReplyTo(f, retry, hello)
	_, other := goHandshake(f, goServerConfig(f), goClientConfig())
	return [][]byte{
		reply,
		retried,
		other,
		reply[:len(reply)/2],
		tlsRecord(21, []byte{2, 40}),
		tlsRecord(22, hsMessage(2, bytes.Repeat([]byte{0}, 38))),
		tlsRecord(22, hsMessage(4, []byte{0, 0, 0, 1})),
		{},
	}
}

// FuzzSelfHostStdX509 feeds hostile bytes to the PEM reader, the X.509 reader
// and the chain verifier.
func FuzzSelfHostStdX509(f *testing.F) {
	fuzzStdReader(f)
}

func x509Seeds(f testing.TB) [][]byte {
	certs := goCertificates(f)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	expired := goCertificate(f, key, []string{"example.com"}, time.Unix(stdparseVerifyTime-1, 0))
	wildcard := goCertificate(f, key, []string{"*.example.com", "example.com", strings.Repeat("a", 250) + ".com"}, time.Unix(stdparseVerifyTime, 0).Add(time.Hour))
	seeds := append([][]byte{}, certs...)
	seeds = append(seeds,
		pemBundle(certs),
		expired,
		wildcard,
		certs[0][:len(certs[0])/2],
		append(append([]byte(nil), certs[1]...), 0),
		[]byte("-----BEGIN CERTIFICATE-----\n!!!!\n-----END CERTIFICATE-----\n"),
		[]byte("-----BEGIN CERTIFICATE-----\nMIIB\n"),
		[]byte{0x30, 0x84, 0xff, 0xff, 0xff, 0xff},
		[]byte{0x30, 0x80, 0, 0},
		[]byte{},
	)
	return seeds
}

// TestSelfHostStdTLSAgainstGo runs the readers on bytes that Go's crypto/tls
// and crypto/x509 wrote, and checks that each reader accepts them.
func TestSelfHostStdTLSAgainstGo(t *testing.T) {
	bin := fuzzStdParse(t)
	run := func(reader string, input []byte) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "input")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}
		r := runUnderBudget(exec.Command(bin, reader, path))
		if failOutsideBudget(t, "stdparse "+reader, r, input) || r.code != 0 {
			t.Fatalf("stdparse %s: exit %d\nstdout: %q\nstderr: %q", reader, r.code, r.stdout, r.stderr)
		}
		return strings.TrimSpace(r.stdout)
	}
	want := func(reader string, input []byte, parts ...string) {
		t.Helper()
		got := run(reader, input)
		for _, p := range parts {
			if !strings.Contains(got, p) {
				t.Errorf("stdparse %s gives %q, want it to contain %q", reader, got, p)
			}
		}
	}
	certs := goCertificates(t)
	for _, c := range certs {
		want("x509", c, "certificates 1 parsed 1 verified 1 names 1")
	}
	want("x509", pemBundle(certs), "certificates 3 parsed 3 verified 3 names 3")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	want("x509", goCertificate(t, key, []string{"example.com"}, time.Unix(stdparseVerifyTime-1, 0)), "parsed 1 verified 0")
	want("x509", goCertificate(t, key, []string{"example.org"}, time.Unix(stdparseVerifyTime, 0).Add(time.Hour)), "parsed 1 verified 0")

	hello, reply := goHandshake(t, goServerConfig(t), goClientConfig())
	want("tls", hello, "tls records 1 rest 0 messages 1 decoded 1", "refused 0 rest 0")
	want("tls-server", hello, "signs 1")
	got := run("tls-client", reply)
	if !strings.HasPrefix(got, "tls-client refused") {
		t.Errorf("stdparse tls-client gives %q on a reply to another client, want a refusal", got)
	}

	own := goReplyTo(t, goServerConfig(t), fernClientHello(t, bin))
	want("tls-client", own, "tls-client connected", "verifies 1")
}
