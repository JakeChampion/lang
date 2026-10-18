package e2eharness

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

// The serve loop's own allocations per request under TLS 1.3
// (`Config.with_tls`): a hello handler on one keep-alive TLS connection,
// the same /count bracketing as CheckServeAllocs, so what one TLS record
// pair (a request record in, a response record out) costs the loop and the
// record layer is read net of the connection's handshake. A ceiling on the
// mean like the HTTP/2 one, since the count moves a little with how the
// client's records coalesce into reads.

// TLSAllocsCert is a self-signed P-256 leaf for localhost and 127.0.0.1,
// its certificate and key as PEM, and a pool trusting it, for a TLS hello
// server the allocations gate drives. One certificate, self-signed, is all
// the record-layer measurement needs.
func TLSAllocsCert(t *testing.T) (certPEM, keyPEM []byte, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certPEM, keyPEM, pool
}

// ServeAllocsTLSServerSource is ServeAllocsServerSource under
// `with_tls`, reading its certificate and key from the files at certPath
// and keyPath (TLSAllocsCert's PEM, written by the gate), so the requests
// arrive on one TLS 1.3 connection.
func ServeAllocsTLSServerSource(port, rounds int, certPath, keyPath string) string {
	return fmt.Sprintf(`import "std/http";
import "std/serve";
import "std/platform";
import "std/tls/identity";
import "std/tls/server";
function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    if (req.path == "/count") {
        return http.ok(__heap_alloc_count().to_string());
    }
    return http.ok("hello");
}
function identity_of(): Result[identity.Identity, string] {
    let cert: string = "";
    let key: string = "";
    match (read_file(%q)) {
        Ok(text) => { cert = text; },
        Err(_) => { return Err("cannot read the certificate"); }
    }
    match (read_file(%q)) {
        Ok(text) => { key = text; },
        Err(_) => { return Err("cannot read the key"); }
    }
    match (identity.from_pem(cert, key)) {
        Ok(id) => { return Ok(id); },
        Err(e) => { return Err(e.message()); }
    }
    return Err("identity");
}
function main(): i32 {
    match (identity_of()) {
        Ok(id) => {
            return serve.run(%d, serve.Config { ...serve.config().with_tls(server.config(id)), keep_alive_requests: %d }, handle);
        },
        Err(e) => {
            eprint("tls alloc server: " + e);
            return 2;
        }
    }
    return 2;
}
`, certPath, keyPath, port, serveAllocsWarm+rounds+8)
}

// CheckServeAllocsTLS is CheckServeAllocs over one keep-alive TLS 1.3
// connection trusting `pool`, against ServeAllocsTLSServerSource. The
// count per request moves a little with how the client's records coalesce
// into the server's reads, so `ceiling` bounds the mean: above it is a
// regression, and a mean serveAllocsH2Drift or more under it lowers the
// ceiling.
func CheckServeAllocsTLS(t *testing.T, addr string, ceiling int64, rounds int, pool *x509.CertPool) {
	t.Helper()
	WaitServerReady(t, addr, 10*time.Second)
	total, seconds := serveAllocsRounds(t, KeepAliveGETTLS(t, addr, pool), rounds)
	checkAllocsCeiling(t, "HTTP/1.1 TLS hello", total, seconds, rounds, ceiling)
}

// KeepAliveGETTLS opens one keep-alive HTTP/1.1-over-TLS connection to the
// server at addr, trusting `pool`, closed when the test ends, and answers a
// GET's body over it, as KeepAliveGET does in the clear.
func KeepAliveGETTLS(t *testing.T, addr string, pool *x509.CertPool) func(path string) string {
	t.Helper()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		RootCAs:    pool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS13,
		MaxVersion: tls.VersionTLS13,
		NextProtos: []string{"http/1.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	reader := bufio.NewReader(conn)
	return func(path string) string {
		if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost\r\nUser-Agent: curl/8.5.0\r\nAccept: */*\r\n\r\n", path); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s answered %d", path, resp.StatusCode)
		}
		return string(body)
	}
}
