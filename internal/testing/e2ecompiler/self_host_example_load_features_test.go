package e2ecompiler

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
	"encoding/csv"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// loadRun runs the tool with args and gives its standard output, its
// standard error and its exit code.
type loadRun func(t *testing.T, args ...string) (string, string, int)

// featureServer is a server of HTTP/1.1 and of HTTP/2 in cleartext (h2c)
// that records each request.
type featureServer struct {
	*httptest.Server
	mu       sync.Mutex
	seen     []*seenRequest
	conns    atomic.Int64
	inFlight atomic.Int64
	most     atomic.Int64
}

type seenRequest struct {
	proto, method, host, uri string
	header                   http.Header
	body                     []byte
	form                     *multipart.Form
}

func (s *featureServer) requests() []*seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*seenRequest(nil), s.seen...)
}

func (s *featureServer) handle(w http.ResponseWriter, r *http.Request) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		m := s.most.Load()
		if n <= m || s.most.CompareAndSwap(m, n) {
			break
		}
	}
	seen := &seenRequest{proto: r.Proto, method: r.Method, host: r.Host, uri: r.RequestURI, header: r.Header.Clone()}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			seen.form = r.MultipartForm
		}
	} else {
		seen.body, _ = io.ReadAll(r.Body)
	}
	s.mu.Lock()
	s.seen = append(s.seen, seen)
	s.mu.Unlock()
	if r.URL.Path == "/slow" {
		time.Sleep(20 * time.Millisecond)
	}
	if r.URL.Path == "/missing" {
		w.WriteHeader(http.StatusNotFound)
	}
	if r.URL.Path == "/late" {
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		time.Sleep(150 * time.Millisecond)
	}
	_, _ = w.Write([]byte("hello\n"))
}

// startFeatureServer starts the server on TCP, with TLS when tls is set.
func startFeatureServer(t *testing.T, tls bool) *featureServer {
	t.Helper()
	s := &featureServer{}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	s.Config.Protocols = p
	s.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.conns.Add(1)
		}
	}
	if tls {
		s.EnableHTTP2 = true
		s.StartTLS()
	} else {
		s.Start()
	}
	t.Cleanup(s.Close)
	return s
}

// alsoOnIPv6Loopback serves s on [::1] at the port of s as well. The
// resolver sorts ::1 before 127.0.0.1 on a host with an IPv6 loopback, so
// "localhost" then goes to ::1. A host with no IPv6 loopback keeps only
// 127.0.0.1 for "localhost", and it gets no second listener.
func (s *featureServer) alsoOnIPv6Loopback(t *testing.T) {
	t.Helper()
	_, port, err := net.SplitHostPort(s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp6", net.JoinHostPort("::1", port))
	if errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("listen on [::1]:%s: %v", port, err)
	}
	if err != nil {
		return
	}
	srv := &http.Server{Handler: s.Config.Handler, Protocols: s.Config.Protocols, ConnState: s.Config.ConnState}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// startUnixServer serves the handler of a feature server on a Unix socket.
func startUnixServer(t *testing.T) (*featureServer, string) {
	t.Helper()
	s := &featureServer{}
	path := filepath.Join(t.TempDir(), "load.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(s.handle)}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return s, path
}

// proxyServer is a forward proxy: a request with an absolute URL goes to
// its host, and a CONNECT opens a tunnel. It speaks h2c too.
type proxyServer struct {
	*httptest.Server
	mu       sync.Mutex
	connects []string
	absolute []string
	protos   []string
	headers  []http.Header
}

func startProxyServer(t *testing.T) *proxyServer {
	t.Helper()
	p := &proxyServer{}
	p.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.protos = append(p.protos, r.Proto)
		p.headers = append(p.headers, r.Header.Clone())
		// A request of HTTP/2 has its host in :authority, not in its URL.
		target := r.URL.String()
		if r.URL.Host == "" {
			target = "http://" + r.Host + r.URL.RequestURI()
		}
		if r.Method == http.MethodConnect {
			p.connects = append(p.connects, r.Host)
		} else {
			p.absolute = append(p.absolute, target)
		}
		p.mu.Unlock()
		if r.Method == http.MethodConnect {
			dst, err := net.Dial("tcp", r.Host)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusOK)
			if r.ProtoMajor == 2 {
				tunnelHTTP2(w, r, dst)
				return
			}
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(dst, buf); _ = dst.Close() }()
			_, _ = io.Copy(conn, dst)
			_ = conn.Close()
			return
		}
		out, err := http.NewRequest(r.Method, target, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out.Header = r.Header.Clone()
		out.Host = r.Host
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	protos := new(http.Protocols)
	protos.SetHTTP1(true)
	protos.SetUnencryptedHTTP2(true)
	p.Config.Protocols = protos
	p.Start()
	t.Cleanup(p.Close)
	return p
}

// tunnelHTTP2 carries the bytes of a CONNECT stream of HTTP/2 to dst and
// back.
func tunnelHTTP2(w http.ResponseWriter, r *http.Request, dst net.Conn) {
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	go func() {
		_, _ = io.Copy(dst, r.Body)
		_ = dst.(*net.TCPConn).CloseWrite()
	}()
	buf := make([]byte, 32<<10)
	for {
		n, err := dst.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			_ = rc.Flush()
		}
		if err != nil {
			break
		}
	}
	_ = dst.Close()
}

// summaryOf runs the tool with args and reads its JSON summary.
func summaryOf(t *testing.T, run loadRun, args ...string) loadJSON {
	t.Helper()
	out, stderr, code := run(t, append(args, "--output-format", "json")...)
	if code != 0 {
		t.Fatalf("load %v: exit %d\n%s%s", args, code, out, stderr)
	}
	return parseLoadJSON(t, out)
}

func protosOf(reqs []*seenRequest) map[string]int {
	out := map[string]int{}
	for _, r := range reqs {
		out[r.proto]++
	}
	return out
}

// loadFeatureTests runs the cases of HTTP/2, proxies, Unix sockets, URLs,
// bodies, signatures, client certificates and outputs.
func loadFeatureTests(t *testing.T, run loadRun) {
	t.Run("http2 cleartext", func(t *testing.T) {
		s := startFeatureServer(t, false)
		j := summaryOf(t, run, "-n", "200", "-c", "2", "-p", "4", "--http2", s.URL+"/slow")
		reqs := s.requests()
		if j.responses() != 200 || len(reqs) != 200 || protosOf(reqs)["HTTP/2.0"] != 200 || s.conns.Load() != 2 {
			t.Errorf("--http2 -c 2 -p 4: %d responses, server %d requests %v on %d connections\n%+v", j.responses(), len(reqs), protosOf(reqs), s.conns.Load(), j.ErrorDistribution)
		}
		if m := s.most.Load(); m < 5 || m > 8 {
			t.Errorf("--http2 -c 2 -p 4 had %d requests in flight at most, want 5 to 8", m)
		}
		if d := j.Details.DNSDialup; d.Average == nil {
			t.Errorf("no DNS+dialup for HTTP/2 requests: %+v", j.Details)
		}
		via := summaryOf(t, run, "-n", "6", "-c", "1", "--http-version", "2", s.URL+"/")
		if via.responses() != 6 {
			t.Errorf("--http-version 2: %d responses", via.responses())
		}
	})

	t.Run("http2 tls", func(t *testing.T) {
		s := startFeatureServer(t, true)
		j := summaryOf(t, run, "-n", "50", "-c", "3", "-p", "2", "--http2", "--insecure", s.URL+"/")
		reqs := s.requests()
		if j.responses() != 50 || protosOf(reqs)["HTTP/2.0"] != 50 || s.conns.Load() != 3 {
			t.Errorf("TLS --http2: %d responses, server %v on %d connections\n%+v", j.responses(), protosOf(reqs), s.conns.Load(), j.ErrorDistribution)
		}
		one := summaryOf(t, run, "-n", "10", "-c", "2", "--insecure", s.URL+"/")
		if one.responses() != 10 || protosOf(s.requests())["HTTP/1.1"] != 10 {
			t.Errorf("TLS without --http2: %v", protosOf(s.requests()))
		}
		statuses := summaryOf(t, run, "-n", "8", "-c", "1", "-p", "3", "--http2", "--insecure", s.URL+"/missing")
		if statuses.StatusCodeDistribution["404"] != 8 {
			t.Errorf("HTTP/2 statuses %v", statuses.StatusCodeDistribution)
		}
	})

	t.Run("http2 duration", func(t *testing.T) {
		s := startFeatureServer(t, false)
		j := summaryOf(t, run, "-z", "300ms", "-c", "2", "-p", "3", "--http2", s.URL+"/slow")
		// HTTP/2 waits for the requests in flight at the end, as in oha.
		if n := j.responses(); n == 0 || int(n) != len(s.requests()) || len(j.ErrorDistribution) != 0 {
			t.Errorf("-z on HTTP/2: %d responses, server %d requests, errors %v", n, len(s.requests()), j.ErrorDistribution)
		}
	})

	t.Run("http 1.0", func(t *testing.T) {
		s := startFeatureServer(t, false)
		j := summaryOf(t, run, "-n", "4", "-c", "1", "--http-version", "1.0", s.URL+"/")
		reqs := s.requests()
		if j.responses() != 4 || protosOf(reqs)["HTTP/1.0"] != 4 || s.conns.Load() != 4 {
			t.Errorf("--http-version 1.0: %d responses, server %v on %d connections", j.responses(), protosOf(reqs), s.conns.Load())
		}
	})

	// As in oha, the first byte is the first data of the body, not the head
	// of the response, on HTTP/1 and on HTTP/2.
	t.Run("first byte", func(t *testing.T) {
		s := startFeatureServer(t, false)
		for _, args := range [][]string{{"-n", "2", "-c", "1"}, {"-n", "2", "-c", "1", "--http2"}} {
			j := summaryOf(t, run, append(args, s.URL+"/late")...)
			fb := j.Details.FirstByte
			if j.responses() != 2 || fb.Fastest == nil || *fb.Fastest < 0.15 {
				t.Errorf("%v: %d responses, first byte %v, want at least 0.15 s", args, j.responses(), fb.Fastest)
			}
		}
	})

	// As in oha, each new connection has the time to the address of its
	// host, from the cache of the lookup before the run, or from a lookup
	// of its own with --no-pre-lookup.
	t.Run("dns lookup", func(t *testing.T) {
		s := startFeatureServer(t, false)
		s.alsoOnIPv6Loopback(t)
		local := strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
		for _, args := range [][]string{{"-n", "4", "-c", "2"}, {"-n", "4", "-c", "2", "--no-pre-lookup"}} {
			j := summaryOf(t, run, append(args, local+"/")...)
			l, d := j.Details.DNSLookup, j.Details.DNSDialup
			if j.responses() != 4 || l.Fastest == nil || d.Slowest == nil || *l.Fastest <= 0 || *l.Slowest > *d.Slowest {
				t.Errorf("%v: %d responses, DNS lookup %v to %v, dialup to %v, errors %v", args, j.responses(), l.Fastest, l.Slowest, d.Slowest, j.ErrorDistribution)
			}
		}
	})

	t.Run("proxy", func(t *testing.T) {
		target := startFeatureServer(t, false)
		secure := startFeatureServer(t, true)
		p := startProxyServer(t)
		j := summaryOf(t, run, "-n", "6", "-c", "2", "-x", p.URL, "--proxy-header", "Proxy-Authorization: Basic eA==", target.URL+"/a?b=1")
		if j.responses() != 6 || len(p.absolute) != 6 || len(target.requests()) != 6 {
			t.Fatalf("-x for http: %d responses, proxy %v, target %d", j.responses(), p.absolute, len(target.requests()))
		}
		if p.absolute[0] != target.URL+"/a?b=1" || p.headers[0].Get("Proxy-Authorization") != "Basic eA==" {
			t.Errorf("the proxy got %q with %v", p.absolute[0], p.headers[0])
		}
		tunnel := summaryOf(t, run, "-n", "5", "-c", "1", "-x", p.URL, "--proxy-header", "X-Proxy: t", "--insecure", secure.URL+"/")
		hostport := strings.TrimPrefix(secure.URL, "https://")
		if tunnel.responses() != 5 || len(p.connects) != 1 || p.connects[0] != hostport || len(secure.requests()) != 5 {
			t.Errorf("-x for https: %d responses, CONNECT %v, target %d", tunnel.responses(), p.connects, len(secure.requests()))
		}
		if p.headers[6].Get("X-Proxy") != "t" || secure.requests()[0].header.Get("X-Proxy") != "" {
			t.Errorf("--proxy-header goes to the CONNECT only: proxy %v, target %v", p.headers[6], secure.requests()[0].header)
		}
		h2 := summaryOf(t, run, "-n", "6", "-c", "1", "-p", "2", "-x", p.URL, "--http2", "--insecure", secure.URL+"/")
		if h2.responses() != 6 || protosOf(secure.requests())["HTTP/2.0"] != 6 {
			t.Errorf("-x --http2 for https: %d responses, target %v", h2.responses(), protosOf(secure.requests()))
		}
		before := len(p.protos)
		ph2 := summaryOf(t, run, "-n", "4", "-c", "1", "-x", p.URL, "--proxy-http2", target.URL+"/h2")
		if ph2.responses() != 4 || p.protos[before] != "HTTP/2.0" || p.absolute[len(p.absolute)-1] != target.URL+"/h2" {
			t.Errorf("--proxy-http2: %d responses, proxy %v %v", ph2.responses(), p.protos[before:], p.absolute)
		}
		for _, inner := range [][]string{nil, {"--http2"}} {
			before, connects, served := len(p.protos), len(p.connects), len(secure.requests())
			args := append([]string{"-n", "6", "-c", "2", "-x", p.URL, "--proxy-http2", "--proxy-header", "X-Proxy: h2", "--insecure"}, append(inner, secure.URL+"/t")...)
			th2 := summaryOf(t, run, args...)
			reqs := secure.requests()[served:]
			want := "HTTP/1.1"
			if inner != nil {
				want = "HTTP/2.0"
			}
			if th2.responses() != 6 || len(p.connects) != connects+2 || p.connects[connects] != hostport || p.protos[before] != "HTTP/2.0" || p.headers[before].Get("X-Proxy") != "h2" || protosOf(reqs)[want] != 6 {
				t.Errorf("--proxy-http2 %v for https: %d responses %v, CONNECT %v over %v, target %v", inner, th2.responses(), th2.ErrorDistribution, p.connects[connects:], p.protos[before:], protosOf(reqs))
			}
		}
	})

	t.Run("unix socket", func(t *testing.T) {
		s, path := startUnixServer(t)
		j := summaryOf(t, run, "-n", "10", "-c", "2", "--unix-socket", path, "http://unix.test/u")
		reqs := s.requests()
		if j.responses() != 10 || len(reqs) != 10 || reqs[0].host != "unix.test" || reqs[0].uri != "/u" {
			t.Errorf("--unix-socket: %d responses, %d requests, first %+v", j.responses(), len(reqs), reqs)
		}
	})

	t.Run("vsock", func(t *testing.T) {
		// Context 2 is the host of a virtual machine, and nothing listens on
		// the port: each connection fails with the errno of a Go reference.
		errno, ok := e2eharness.VsockConnectErrno(2, 45391)
		if !ok || errno == 0 {
			t.Fatalf("the VSOCK reference gave errno %d, ok %v", errno, ok)
		}
		j := summaryOf(t, run, "-n", "2", "-c", "1", "--vsock-addr", "2:45391", "http://vsock.test/")
		want := fmt.Sprintf("(os error %d)", errno)
		if j.responses() != 0 || len(j.ErrorDistribution) != 1 {
			t.Fatalf("--vsock-addr: %d responses, errors %v", j.responses(), j.ErrorDistribution)
		}
		for e, n := range j.ErrorDistribution {
			if !strings.HasSuffix(e, want) || n != 2 {
				t.Errorf("--vsock-addr: %q %d times, want 2 errors ending in %q", e, n, want)
			}
		}
	})

	t.Run("generated urls", func(t *testing.T) {
		s := startFeatureServer(t, false)
		pattern := s.URL + "/[a-c]/x{1,3}"
		j := summaryOf(t, run, "-n", "300", "-c", "3", "--rand-regex-url", pattern)
		paths := map[string]int{}
		valid := regexp.MustCompile(`^/[a-c]/x{1,3}$`)
		for _, r := range s.requests() {
			if !valid.MatchString(r.uri) {
				t.Fatalf("--rand-regex-url sent %q", r.uri)
			}
			paths[r.uri[:2]]++
		}
		if j.responses() != 300 || len(paths) != 3 {
			t.Fatalf("--rand-regex-url: %d responses over %v", j.responses(), paths)
		}
		for p, n := range paths {
			if n < 60 || n > 140 {
				t.Errorf("--rand-regex-url drew %s %d times of 300, want about 100", p, n)
			}
		}
		list := filepath.Join(t.TempDir(), "urls.txt")
		if err := os.WriteFile(list, []byte(s.URL+"/one\n\n"+s.URL+"/two\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := len(s.requests())
		f := summaryOf(t, run, "-n", "40", "-c", "2", "--urls-from-file", list)
		got := map[string]int{}
		for _, r := range s.requests()[before:] {
			got[r.uri]++
		}
		if f.responses() != 40 || got["/one"] < 8 || got["/two"] < 8 || len(got) != 2 {
			t.Errorf("--urls-from-file: %d responses over %v", f.responses(), got)
		}
		out, stderr, code := run(t, "--dump-urls", "5", "--rand-regex-url", "--max-repeat", "2", "http://h.test/[0-9]z*")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		dumped := regexp.MustCompile(`^http://h\.test/[0-9]z{0,2}$`)
		if code != 0 || len(lines) != 5 {
			t.Fatalf("--dump-urls 5: exit %d, %q %s", code, out, stderr)
		}
		for _, l := range lines {
			if !dumped.MatchString(l) {
				t.Errorf("--dump-urls wrote %q", l)
			}
		}
		if _, stderr, code := run(t, "--rand-regex-url", "-n", "1", "http://h/^a"); code != 1 || !strings.Contains(stderr, "anchor is not supported") {
			t.Errorf("an anchor: exit %d, %q", code, stderr)
		}
	})

	t.Run("bodies", func(t *testing.T) {
		s := startFeatureServer(t, false)
		dir := t.TempDir()
		lines := filepath.Join(dir, "lines")
		if err := os.WriteFile(lines, []byte("one\ntwo\r\nthree"), 0o644); err != nil {
			t.Fatal(err)
		}
		j := summaryOf(t, run, "-n", "60", "-c", "2", "-m", "POST", "-Z", lines, s.URL+"/z")
		bodies := map[string]int{}
		for _, r := range s.requests() {
			bodies[string(r.body)]++
		}
		if j.responses() != 60 || len(bodies) != 3 || bodies["one"] == 0 || bodies["two"] == 0 || bodies["three"] == 0 {
			t.Errorf("-Z: %d responses, bodies %v", j.responses(), bodies)
		}
		upload := filepath.Join(dir, "up.txt")
		if err := os.WriteFile(upload, []byte("file data"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := len(s.requests())
		f := summaryOf(t, run, "-n", "3", "-c", "1", "-F", "name=value", "-F", "file=@"+upload+";type=text/x-load", s.URL+"/f")
		reqs := s.requests()[before:]
		if f.responses() != 3 || len(reqs) != 3 || reqs[0].method != "POST" || reqs[0].form == nil {
			t.Fatalf("-F: %d responses, %+v", f.responses(), reqs)
		}
		form := reqs[0].form
		files := form.File["file"]
		if form.Value["name"][0] != "value" || len(files) != 1 || files[0].Filename != "up.txt" || files[0].Header.Get("Content-Type") != "text/x-load" {
			t.Errorf("-F: values %v, files %+v", form.Value, files)
		}
	})

	t.Run("aws sigv4", func(t *testing.T) {
		s := startFeatureServer(t, false)
		j := summaryOf(t, run, "-n", "3", "-c", "1", "-a", "AKID:SECRET", "--aws-session", "TOKEN", "--aws-sigv4", "aws:amz:eu-west-1:s3", "-H", "X-Custom: v", "-d", "body", s.URL+"/p/q?b=2&a=1")
		reqs := s.requests()
		if j.responses() != 3 || len(reqs) != 3 {
			t.Fatalf("--aws-sigv4: %d responses, %d requests", j.responses(), len(reqs))
		}
		for _, r := range reqs {
			if why := checkSigV4(r, "SECRET"); why != "" {
				t.Errorf("--aws-sigv4: %s\n%v", why, r.header)
			}
		}
		if reqs[0].header.Get("X-Amz-Security-Token") != "TOKEN" || !strings.HasPrefix(reqs[0].header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") {
			t.Errorf("--aws-sigv4 fields %v", reqs[0].header)
		}
	})

	t.Run("client certificate", func(t *testing.T) {
		dir := t.TempDir()
		ca, caKey := makeCA(t)
		cert, key := makeClientCert(t, ca, caKey)
		certFile, keyFile := filepath.Join(dir, "client.pem"), filepath.Join(dir, "client.key")
		if err := os.WriteFile(certFile, cert, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyFile, key, 0o600); err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		pool.AddCert(ca)
		var subjects atomic.Value
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.TLS.PeerCertificates) > 0 {
				subjects.Store(r.TLS.PeerCertificates[0].Subject.CommonName)
			}
			_, _ = w.Write([]byte("ok"))
		}))
		srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
		srv.StartTLS()
		defer srv.Close()
		j := summaryOf(t, run, "-n", "4", "-c", "2", "--insecure", "--cert", certFile, "--key", keyFile, srv.URL+"/")
		if j.responses() != 4 || subjects.Load() != "load client" {
			t.Errorf("--cert and --key: %d responses, errors %v, subject %v", j.responses(), j.ErrorDistribution, subjects.Load())
		}
		without := summaryOf(t, run, "-n", "2", "-c", "1", "--insecure", srv.URL+"/")
		if without.responses() != 0 || len(without.ErrorDistribution) == 0 {
			t.Errorf("no client certificate: %d responses, errors %v", without.responses(), without.ErrorDistribution)
		}
		if _, stderr, code := run(t, "-n", "1", "--cert", keyFile, "--key", keyFile, srv.URL+"/"); code != 1 || !strings.Contains(stderr, "--cert") {
			t.Errorf("a key as --cert: exit %d, %q", code, stderr)
		}
		subjects.Store("")
		srv12 := httptest.NewUnstartedServer(srv.Config.Handler)
		srv12.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MaxVersion: tls.VersionTLS12}
		srv12.StartTLS()
		defer srv12.Close()
		j12 := summaryOf(t, run, "-n", "4", "-c", "2", "--insecure", "--cert", certFile, "--key", keyFile, srv12.URL+"/")
		if j12.responses() != 4 || subjects.Load() != "load client" {
			t.Errorf("--cert and --key over TLS 1.2: %d responses, errors %v, subject %v", j12.responses(), j12.ErrorDistribution, subjects.Load())
		}
	})

	t.Run("csv", func(t *testing.T) {
		s := startFeatureServer(t, false)
		out, stderr, code := run(t, "-n", "20", "-c", "2", "--output-format", "csv", s.URL+"/")
		if code != 0 {
			t.Fatalf("csv: exit %d %s", code, stderr)
		}
		rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
		if err != nil || len(rows) != 21 {
			t.Fatalf("csv: %v, %d rows\n%s", err, len(rows), out)
		}
		if strings.Join(rows[0], ",") != "request-start,DNS,DNS+dialup,Response-delay,request-duration,bytes,status" {
			t.Errorf("csv header %v", rows[0])
		}
		last := -1.0
		for _, r := range rows[1:] {
			var start, dns, dial, delay, dur float64
			var size, status int
			if _, err := fmt.Sscanf(strings.Join(r, " "), "%g %g %g %g %g %d %d", &start, &dns, &dial, &delay, &dur, &size, &status); err != nil || start < last || status != 200 || size != 6 || delay > dur || dur <= 0 {
				t.Errorf("csv row %v: %v", r, err)
			}
			last = start
		}
	})

	t.Run("db", func(t *testing.T) {
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Skip("python3 reads the SQLite file")
		}
		s := startFeatureServer(t, false)
		db := filepath.Join(t.TempDir(), "results.db")
		for _, args := range [][]string{{"-n", "5", "-c", "1"}, {"-n", "3", "-c", "1", "-q", "200", "--latency-correction"}} {
			if out, stderr, code := run(t, append(args, "--db-url", db, s.URL+"/d")...); code != 0 || !strings.Contains(stderr, "Storing results to "+db) {
				t.Fatalf("--db-url: exit %d\n%s%s", code, out, stderr)
			}
		}
		script := `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
assert c.execute("pragma integrity_check").fetchone()[0] == "ok"
rows = c.execute("select url, start, start_latency_correction, end, duration, status, len_bytes, run from oha order by rowid").fetchall()
assert len(rows) == 8, rows
for i, r in enumerate(rows):
    assert r[0] == "/d" and r[5] == 200 and r[6] == 6 and r[3] >= r[1] and r[4] > 0, r
    assert (r[2] is None) == (i < 5), r
print("ok")
`
		if out, err := exec.Command(python, "-c", script, db).CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "ok" {
			t.Errorf("the database: %v\n%s", err, out)
		}
		other := filepath.Join(t.TempDir(), "other.db")
		if out, err := exec.Command(python, "-c", "import sqlite3, sys; c = sqlite3.connect(sys.argv[1]); c.execute('create table t (x)'); c.commit()", other).CombinedOutput(); err != nil {
			t.Fatalf("python: %v %s", err, out)
		}
		if _, stderr, code := run(t, "-n", "1", "--db-url", other, s.URL+"/"); code != 1 || !strings.Contains(stderr, "more than the table oha") && !strings.Contains(stderr, "not the table oha") {
			t.Errorf("a database with another table: exit %d, %q", code, stderr)
		}
	})

	t.Run("breakdown", func(t *testing.T) {
		s := startFeatureServer(t, false)
		out, stderr, code := run(t, "-n", "6", "-c", "1", "--stats-success-breakdown", s.URL+"/missing")
		if code != 0 || !strings.Contains(out, "\nResponse time histogram (2xx only):\n") || !strings.Contains(out, "\nResponse time distribution (4xx + 5xx only):\n") {
			t.Errorf("--stats-success-breakdown: exit %d\n%s%s", code, out, stderr)
		}
	})

	t.Run("debug", func(t *testing.T) {
		s := startFeatureServer(t, false)
		out, stderr, code := run(t, "--debug", "-d", "x", s.URL+"/dbg")
		if code != 0 || !strings.HasPrefix(out, "Request {\n    method: GET,\n    uri: /dbg,\n    version: HTTP/1.1,\n") || !strings.Contains(out, "\nResponse {\n    status: 200,\n") || !strings.HasSuffix(out, "    body: b\"hello\\n\",\n}\n") || len(s.requests()) != 1 {
			t.Errorf("--debug: exit %d, %d requests\n%s%s", code, len(s.requests()), out, stderr)
		}
	})
}

// loadH3Server is a std/serve server of HTTP/3: PORT CERT KEY.
const loadH3Server = `import "std/http";
import "std/i32";
import "std/platform";
import "std/rand";
import "std/result";
import "std/serve";
import "std/time";
import "std/tls/identity";
import "std/tls/server";

function handle(req: HttpRequest, plat: platform.Host): HttpResponse {
  if (req.path == "/slow") {
    plat.sleep(time.duration_millis(20 as i64));
  }
  if (req.path == "/missing") {
    return http.text(404, "missing\n");
  }
  return http.ok("hello\n");
}

function main(): i32 {
  let argv = args();
  let port = i32.parse_i32(argv[1]).unwrap_or(0);
  let Ok(cert) = read_file(argv[2]) else {
    return 2;
  };
  let Ok(key) = read_file(argv[3]) else {
    return 3;
  };
  let Ok(id) = identity.from_pem(cert, key) else {
    return 4;
  };
  let Ok(tickets) = server.config(id, rand.bytes(32)) else { return 2; };
  let Ok(secure) = serve.config().with_tls(tickets) else { return 2; };
  return serve.exit_code(serve.run(port, secure.with_h3(), handle));
}
`

// startH3Server starts the server of HTTP/3 at bin, with a certificate for
// 127.0.0.1, and gives its URL and the PEM file of the certificate.
func startH3Server(t *testing.T, bin string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0o600)
	// The server takes the TCP and the UDP port of one number. Another
	// process can hold the TCP port of a free UDP port, and then the server
	// stops, so the start tries again with a new port.
	for try := 0; try < 5; try++ {
		l, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.LocalAddr().(*net.UDPAddr).Port
		_ = l.Close()
		cmd := exec.Command(bin, fmt.Sprint(port), cert, keyFile)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		if h3Ready(port, exited) {
			t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
			return fmt.Sprintf("https://127.0.0.1:%d", port), cert
		}
		_ = cmd.Process.Kill()
		<-exited
	}
	t.Fatal("the HTTP/3 server did not start")
	return "", ""
}

// h3Ready waits for the server on port to take a TCP connection. It gives
// false when the server stops first, or after 30 seconds.
func h3Ready(port int, exited chan struct{}) bool {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return false
		default:
		}
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// loadH3Tests runs the tool with --http-version 3 against a std/serve
// server of HTTP/3.
func loadH3Tests(t *testing.T, run loadRun, server string) {
	url, cert := startH3Server(t, server)
	j := summaryOf(t, run, "-n", "200", "-c", "2", "-p", "4", "--http-version", "3", "--insecure", url+"/")
	if j.responses() != 200 || j.StatusCodeDistribution["200"] != 200 || len(j.ErrorDistribution) != 0 || j.Details.DNSDialup.Average == nil {
		t.Errorf("--http-version 3: %d responses, %v, errors %v", j.responses(), j.StatusCodeDistribution, j.ErrorDistribution)
	}
	started := time.Now()
	verified := summaryOf(t, run, "-n", "10", "-c", "1", "--http-version", "3", "--cacert", cert, url+"/missing")
	verifiedTime := time.Since(started)
	if verified.StatusCodeDistribution["404"] != 10 {
		t.Errorf("--http-version 3 --cacert: %v, errors %v", verified.StatusCodeDistribution, verified.ErrorDistribution)
	}
	untrusted := summaryOf(t, run, "-n", "2", "-c", "1", "--http-version", "3", url+"/")
	if untrusted.responses() != 0 || len(untrusted.ErrorDistribution) == 0 {
		t.Errorf("HTTP/3 with an untrusted certificate: %d responses", untrusted.responses())
	}
	// The duration includes the handshake. Allow the two connections time
	// to establish on this runner before the delayed responses arrive.
	duration := 300*time.Millisecond + 2*verifiedTime
	timed := summaryOf(t, run, "-z", fmt.Sprintf("%dms", duration.Milliseconds()), "-c", "2", "-p", "3", "--http-version", "3", "--insecure", url+"/slow")
	if timed.responses() == 0 || len(timed.ErrorDistribution) != 0 {
		t.Errorf("-z %s with HTTP/3: %d responses, errors %v", duration, timed.responses(), timed.ErrorDistribution)
	}
	out, _, code := run(t, "--debug", "--http-version", "3", "--insecure", url+"/")
	if code != 0 || !strings.Contains(out, "\nResponse {\n    status: 200,\n    version: HTTP/3.0,\n") {
		t.Errorf("--debug with HTTP/3: exit %d\n%s", code, out)
	}
}

// ptyScript runs a command in a pseudo-terminal of 30 rows and 100 columns
// and writes what the command wrote. With a first argument other than "-",
// it types that key after half a second.
const ptyScript = `import fcntl, os, pty, select, struct, sys, termios, time
key, cmd = sys.argv[1], sys.argv[2:]
pid, fd = pty.fork()
if pid == 0:
    os.execvp(cmd[0], cmd)
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
out, start, sent = b"", time.time(), key == "-"
while time.time() - start < 20:
    if not sent and time.time() - start > 0.5:
        os.write(fd, key.encode())
        sent = True
    r, _, _ = select.select([fd], [], [], 0.05)
    if r:
        try:
            chunk = os.read(fd, 65536)
        except OSError:
            break
        if not chunk:
            break
        out += chunk
_, status = os.waitpid(pid, 0)
sys.stdout.buffer.write(out)
sys.exit(os.waitstatus_to_exitcode(status))
`

// inTerminal runs the command in a pseudo-terminal, and types `key` unless
// it is "-".
func inTerminal(t *testing.T, python, key string, cmd ...string) string {
	t.Helper()
	out, err := exec.Command(python, append([]string{"-c", ptyScript, key}, cmd...)...).Output()
	if err != nil {
		t.Fatalf("%v in a terminal: %v\n%q", cmd, err, out)
	}
	return string(out)
}

// loadTerminalTests runs the tool on a terminal: the live display of oha,
// and the colors of its summary. bin is the tool, or for arm64 the command
// of qemu and the tool.
func loadTerminalTests(t *testing.T, bin []string) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 makes the pseudo-terminal")
	}
	s := startFeatureServer(t, false)
	t.Run("live display", func(t *testing.T) {
		out := inTerminal(t, python, "-", append(bin, "-n", "300", "-c", "2", "-q", "500", s.URL+"/")...)
		for _, want := range []string{"\x1b[?1049h", "┌Progress", "┌Stats for last", "Requests : ", "┌Status code distribution", "[200] ", "┌Error distribution", "┌Requests / past", "(auto). press -/+/a to change", "┌Response time histogram", "\x1b[?1049l", "Status code distribution:"} {
			if !strings.Contains(out, want) {
				t.Errorf("the display has no %q\n%q", want, out)
			}
		}
		if strings.Index(out, "\x1b[?1049l") > strings.Index(out, "Summary:") {
			t.Errorf("the summary comes before the display ends\n%q", out)
		}
	})
	t.Run("quit", func(t *testing.T) {
		started := time.Now()
		out := inTerminal(t, python, "q", append(bin, "-z", "10s", "-c", "1", "-q", "100", s.URL+"/")...)
		if time.Since(started) > 5*time.Second || !strings.Contains(out, "Summary:") || !strings.Contains(out, "[200] ") {
			t.Errorf("q did not stop the run with a summary after %v\n%q", time.Since(started), out)
		}
	})
	t.Run("colors", func(t *testing.T) {
		out := inTerminal(t, python, "-", append(bin, "--no-tui", "-n", "20", "-c", "1", s.URL+"/")...)
		for _, want := range []string{"\x1b[1m\x1b[4mSummary:\x1b[0m", "\x1b[38;5;10m\x1b[1m  Success rate:\t100.00%\x1b[0m", "\x1b[38;5;11m  Slowest:\t", "\x1b[38;5;10m  [200] 20 responses\x1b[39m"} {
			if !strings.Contains(out, want) {
				t.Errorf("the summary on a terminal has no %q\n%q", want, out)
			}
		}
		if strings.Contains(out, "┌Progress") {
			t.Errorf("--no-tui showed the display\n%q", out)
		}
		plain := inTerminal(t, python, "-", append(bin, "--no-tui", "--no-color", "-n", "5", "-c", "1", s.URL+"/")...)
		if strings.Contains(plain, "\x1b[") {
			t.Errorf("--no-color has colors\n%q", plain)
		}
	})
}

// checkSigV4 checks the AWS signature of r with the rules of the
// aws-sign-v4 crate of oha, and gives what is wrong, or "".
func checkSigV4(r *seenRequest, secret string) string {
	auth := r.header.Get("Authorization")
	m := regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=([^/]+)/(\d{8})/([^/]+)/([^/]+)/aws4_request,SignedHeaders=([^,]+),Signature=([0-9a-f]{64})$`).FindStringSubmatch(auth)
	if m == nil {
		return "no signature in " + auth
	}
	date, region, service, signed, got := m[2], m[3], m[4], strings.Split(m[5], ";"), m[6]
	stamp := r.header.Get("X-Amz-Date")
	var lines []string
	for _, name := range signed {
		value := r.header.Get(name)
		switch name {
		case "host":
			value = r.host
		case "content-length":
			// net/http keeps the length out of the header.
			value = fmt.Sprint(len(r.body))
		}
		lines = append(lines, name+":"+strings.TrimSpace(value))
	}
	sort.Strings(lines)
	u, err := url.ParseRequestURI(r.uri)
	if err != nil {
		return err.Error()
	}
	var pairs []string
	for k, vs := range u.Query() {
		for _, v := range vs {
			pairs = append(pairs, awsEncode(k)+"="+awsEncode(v))
		}
	}
	sort.Strings(pairs)
	sum := sha256.Sum256(r.body)
	canonical := r.method + "\n" + u.EscapedPath() + "\n" + strings.Join(pairs, "&") + "\n" + strings.Join(lines, "\n") + "\n\n" + strings.Join(signed, ";") + "\n" + hex.EncodeToString(sum[:])
	scope := date + "/" + region + "/" + service + "/aws4_request"
	creq := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(creq[:])
	key := []byte("AWS4" + secret)
	for _, part := range []string{date, region, service, "aws4_request"} {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	if want := hex.EncodeToString(mac.Sum(nil)); want != got {
		return fmt.Sprintf("signature %s, want %s for\n%s", got, want, canonical)
	}
	return ""
}

func awsEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '~' || c == '.' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func makeCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "load test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return ca, key
}

func makeClientCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "load client"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
}

// captureServer answers each request with a fixed response, and keeps the
// bytes of each request.
type captureServer struct {
	addr string
	mu   sync.Mutex
	got  [][]byte
}

func startCaptureServer(t *testing.T) *captureServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := &captureServer{addr: l.Addr().String()}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go c.serve(conn)
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return c
}

func (c *captureServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var head bytes.Buffer
		length, chunked := 0, false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			head.WriteString(line)
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "content-length:") {
				fmt.Sscanf(strings.TrimSpace(line[15:]), "%d", &length)
			}
			if strings.HasPrefix(lower, "transfer-encoding:") && strings.Contains(lower, "chunked") {
				chunked = true
			}
			if line == "\r\n" {
				break
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		if chunked {
			for !bytes.HasSuffix(body, []byte("0\r\n\r\n")) {
				b, err := r.ReadByte()
				if err != nil {
					return
				}
				body = append(body, b)
			}
		}
		c.mu.Lock()
		c.got = append(c.got, append(head.Bytes(), body...))
		c.mu.Unlock()
		if strings.HasPrefix(head.String(), "CONNECT ") {
			_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
		return
	}
}

func (c *captureServer) take() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.got
	c.got = nil
	return out
}

// ohaPath is oha 1.16, which `eval "$(scripts/toolchain-env)"` installs
// from mise.toml.
func ohaPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("oha")
	if err != nil {
		t.Skip("oha is not installed: eval \"$(scripts/toolchain-env)\" installs the pin of mise.toml")
	}
	if out, err := exec.Command(p, "--version").Output(); err != nil || strings.TrimSpace(string(out)) != "oha 1.16.0" {
		t.Skipf("oha 1.16.0 is not on PATH: %s", out)
	}
	return p
}

func runOha(t *testing.T, oha string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(oha, append([]string{"--no-tui"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

var formBoundary = regexp.MustCompile(`----formdata-(oha|load)-[0-9a-f]{32}`)

var contentLength = regexp.MustCompile(`(?m)^content-length: [0-9]+`)

// wireForm is the bytes of a request with the user agent of oha and one
// boundary for a form, with the Content-Length of that body.
func wireForm(b []byte) string {
	s := strings.ReplaceAll(string(b), "user-agent: fern-load/0.4", "user-agent: oha/1.16.0")
	if !formBoundary.MatchString(s) {
		return s
	}
	s = formBoundary.ReplaceAllString(s, "BOUNDARY")
	head, body, _ := strings.Cut(s, "\r\n\r\n")
	return contentLength.ReplaceAllString(head, fmt.Sprintf("content-length: %d", len(body))) + "\r\n\r\n" + body
}

// loadOhaTests compares the tool with oha 1.16 where the output does not
// depend on time: the exit codes of the options, the bytes of requests, the
// URLs of --dump-urls, the fields of csv and JSON, and --debug.
func loadOhaTests(t *testing.T, run loadRun) {
	oha := ohaPath(t)
	refused := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return "http://" + l.Addr().String() + "/"
	}()

	t.Run("exit codes", func(t *testing.T) {
		for _, args := range [][]string{
			{"-n", "2", "-c", "1"}, {"-n", "0"}, {"-n", "2", "-z", "1s"}, {"-w"}, {"-q", "0", "-n", "1"},
			{"--burst-delay", "1s", "-n", "1"}, {"--burst-rate", "2", "-n", "1"}, {"-q", "5", "--burst-delay", "1s", "--burst-rate", "2", "-n", "1"},
			{"--max-repeat", "3", "-n", "1"}, {"--http2", "--http-version", "2", "-n", "1"}, {"--http-version", "1.0", "-n", "1"},
			{"--http-version", "4", "-n", "1"}, {"-d", "a", "-D", "b", "-n", "1"}, {"-F", "a=b", "-d", "x", "-n", "1"}, {"--cert", "a", "-n", "1"},
			{"--unix-socket", "/x", "--vsock-addr", "1:2", "-n", "1"}, {"--vsock-addr", "1", "-n", "1"}, {"--worker-threads", "0", "-n", "1"},
			{"-H", "nocolon", "-n", "1"}, {"-m", "GE T", "-n", "1"}, {"-n", "1.5k", "-c", "1"}, {"-z", "10", "-n", "1"}, {"--output-format", "xml"},
			{"-u", "x"}, {"--connect-to", "bad", "-n", "1"}, {"--aws-sigv4", "aws:amz:r:s", "-n", "1"}, {"--aws-sigv4", "bad", "-a", "a:b", "-n", "1"},
			{"-n", "1", "-n", "2"}, {"--insecure", "--insecure", "-n", "1"}, {"-n", "-5"}, {"-x", "notaurl", "-n", "1"}, {"-a", "nocolon", "-n", "1"},
			{"-n", "3", "-c", "1", "-Z", "/nonexistent"}, {"-A", "a\x01", "-n", "1"}, {"--rand-regex-url", "-n", "1", "--max-repeat", "2"},
			{"--http-version", "0.9", "-n", "2"}, {"--proxy-http-version", "0.9", "-n", "2"},
		} {
			full := append(append([]string{}, args...), refused)
			if args[0] == "--rand-regex-url" {
				full = append(args, strings.TrimSuffix(refused, "/")+"/^a")
			}
			_, ohaErr, want := runOha(t, oha, full...)
			_, ourErr, got := run(t, full...)
			if got != want {
				t.Errorf("%v: exit %d, oha %d\n%s\noha: %s", args, got, want, ourErr, ohaErr)
			}
		}
	})

	// oha 1.16.0 accepts HTTP/0.9, and hyper panics when it writes the
	// first request on an open connection. Both abort with the same text.
	t.Run("http 0.9", func(t *testing.T) {
		c := startCaptureServer(t)
		base := "http://" + c.addr
		thread := regexp.MustCompile(`\(\d+\)`)
		for _, args := range [][]string{
			{"--http-version", "0.9", base + "/"},
			{"--proxy-http-version", "0.9", "-x", base, "http://127.0.0.1:1/p"},
		} {
			full := append([]string{"-n", "2", "-c", "1"}, args...)
			ohaOut, ohaErr, want := runOha(t, oha, full...)
			ourOut, ourErr, got := run(t, full...)
			head := func(s string) string {
				lines := strings.SplitN(thread.ReplaceAllString(s, "(N)"), "\n", 4)
				return strings.Join(lines[:min(3, len(lines))], "\n")
			}
			if got != want || head(ourErr) != head(ohaErr) || ourOut != ohaOut {
				t.Errorf("%v: exit %d, stdout %q, stderr %q\noha: exit %d, stdout %q, stderr %q", args, got, ourOut, ourErr, want, ohaOut, ohaErr)
			}
			if len(c.take()) != 0 {
				t.Errorf("%v: a request reached the server", args)
			}
		}
	})

	t.Run("request bytes", func(t *testing.T) {
		c := startCaptureServer(t)
		dir := t.TempDir()
		upload := filepath.Join(dir, "up.txt")
		if err := os.WriteFile(upload, []byte("data\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		base := "http://" + c.addr
		for _, args := range [][]string{
			{base + "/"},
			{"-d", "abcdef", base + "/"},
			{"-m", "POST", base + "/x"},
			{"-d", "abcdef", "-H", "Content-Length: 3", "-H", "X-A: b", base + "/"},
			{"-H", "Transfer-Encoding: chunked", "-d", "abcdef", base + "/"},
			{"-A", "text/x", "-T", "app/y", "-a", "u:p", "--host", "vh", "-H", "accept: other", "-H", "X-Z:   sp  ", "--disable-keepalive", "--disable-compression", base + "/a"},
			{"-a", "user:", base + "/"},
			{"--http-version", "1.0", base + "/"},
			{"-F", "a=b", "-F", "f=@" + upload + ";type=text/plain", base + "/"},
			{"--no-pre-lookup", "-x", base, "--proxy-header", "Proxy-Authorization: Basic eA==", "http://example.invalid:8080/q"},
			{"--no-pre-lookup", "-x", base, "--connect-to", "example.invalid:80:127.0.0.9:81", "http://example.invalid/r"},
			{"--no-pre-lookup", "-x", base, "--proxy-header", "Proxy-Authorization: Basic eA==", "https://example.invalid/s"},
		} {
			full := append([]string{"-n", "1", "-c", "1"}, args...)
			runOha(t, oha, full...)
			want := c.take()
			run(t, full...)
			got := c.take()
			if len(want) != 1 || len(got) != 1 || wireForm(got[0]) != wireForm(want[0]) {
				t.Errorf("%v:\n got %q\nwant %q", args, got, want)
			}
		}
	})

	t.Run("dump urls", func(t *testing.T) {
		pattern := `http://h.test/(ab|c)[x-z]{2,3}/q\?k=[0-9]`
		ohaOut, _, _ := runOha(t, oha, "--dump-urls", "400", "--rand-regex-url", pattern)
		ourOut, _, code := run(t, "--dump-urls", "400", "--rand-regex-url", pattern)
		valid := regexp.MustCompile(`^http://h\.test/(ab|c)[x-z]{2,3}/q\?k=[0-9]$`)
		share := func(out string) (map[string]int, bool) {
			counts := map[string]int{}
			lines := strings.Split(strings.TrimSpace(out), "\n")
			for _, l := range lines {
				if !valid.MatchString(l) {
					return nil, false
				}
				m := valid.FindStringSubmatch(l)
				counts[m[1]]++
				counts[fmt.Sprint(len(l))]++
			}
			return counts, len(lines) == 400
		}
		ours, ok1 := share(ourOut)
		theirs, ok2 := share(ohaOut)
		if code != 0 || !ok1 || !ok2 {
			t.Fatalf("--dump-urls: exit %d\n%s\noha:\n%s", code, ourOut, ohaOut)
		}
		for k, n := range theirs {
			if d := ours[k] - n; d > 70 || d < -70 {
				t.Errorf("--dump-urls: %s drawn %d times, oha %d", k, ours[k], n)
			}
		}
	})

	t.Run("outputs", func(t *testing.T) {
		s := startFeatureServer(t, false)
		for _, format := range [][]string{{"--output-format", "csv"}, {"--output-format", "json", "--stats-success-breakdown"}} {
			args := append([]string{"-n", "5", "-c", "1"}, append(format, s.URL+"/")...)
			ohaOut, _, _ := runOha(t, oha, args...)
			ourOut, _, _ := run(t, args...)
			if shapeOf(ourOut) != shapeOf(ohaOut) {
				t.Errorf("%v:\n got shape %s\nwant shape %s", format, shapeOf(ourOut), shapeOf(ohaOut))
			}
		}
		ohaOut, _, _ := runOha(t, oha, "-n", "2", "-c", "1", refused)
		ourOut, _, _ := run(t, "-n", "2", "-c", "1", refused)
		if textShape(ourOut) != textShape(ohaOut) {
			t.Errorf("a run with no response:\n got %s\nwant %s", ourOut, ohaOut)
		}
		ohaDebug, _, _ := runOha(t, oha, "--debug", "-d", "a\"b\\n", "-H", "X-Q: é", s.URL+"/dbg")
		ourDebug, _, _ := run(t, "--debug", "-d", "a\"b\\n", "-H", "X-Q: é", s.URL+"/dbg")
		date := regexp.MustCompile(`"date": "[^"]*"`)
		norm := func(s string) string {
			return date.ReplaceAllString(strings.ReplaceAll(s, "fern-load/0.4", "oha/1.16.0"), `"date": "-"`)
		}
		if norm(ourDebug) != norm(ohaDebug) {
			t.Errorf("--debug:\n got %s\nwant %s", ourDebug, ohaDebug)
		}
		vsock := []string{"-n", "2", "-c", "1", "--output-format", "json", "--vsock-addr", "2:45391", "http://vsock.test/"}
		ohaVsock, _, ohaCode := runOha(t, oha, vsock...)
		ourVsock, _, ourCode := run(t, vsock...)
		osError := regexp.MustCompile(`\(os error \d+\)": \d+`)
		if ourCode != ohaCode || osError.FindString(ourVsock) == "" || osError.FindString(ourVsock) != osError.FindString(ohaVsock) {
			t.Errorf("--vsock-addr: exit %d and %s, oha exit %d and %s", ourCode, ourVsock, ohaCode, ohaVsock)
		}
	})
}

// shapeOf is the shape of an output of csv or JSON: the header line of csv,
// or the keys of each object of JSON in order, with the numbers and texts
// left out.
func shapeOf(out string) string {
	if strings.HasPrefix(out, "request-start") {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		return fmt.Sprintf("%s rows=%d", lines[0], len(lines)-1)
	}
	var keys []string
	key := regexp.MustCompile(`^\s*"([A-Za-z_.0-9]+)": [{\[]?`)
	for _, line := range strings.Split(out, "\n") {
		if m := key.FindStringSubmatch(line); m != nil && !regexp.MustCompile(`^[0-9.e-]+$`).MatchString(m[1]) {
			keys = append(keys, m[1])
		}
	}
	return strings.Join(keys, " ")
}

// textShape is a text summary with each number and the errors left out.
func textShape(out string) string {
	if i := strings.Index(out, "Error distribution:"); i >= 0 {
		out = out[:i]
	}
	return regexp.MustCompile(`[0-9]+\.[0-9]+`).ReplaceAllString(out, "N")
}

// resetAfterAlertConn is a server connection that resets after its alert.
// The tool is stopped while the alert and the reset go to it, so that it
// reads the alert and then writes to a connection that is reset.
type resetAfterAlertConn struct {
	*net.TCPConn
	client func() *os.Process
	writes atomic.Int32
}

// The first write is the flight of the handshake, and the second write is
// the alert that refuses the client.
func (c *resetAfterAlertConn) Write(b []byte) (int, error) {
	if c.writes.Add(1) == 2 {
		if p := c.client(); p != nil {
			_ = p.Signal(syscall.SIGSTOP)
		}
	}
	return c.TCPConn.Write(b)
}

func (c *resetAfterAlertConn) Close() error {
	_ = c.TCPConn.SetLinger(0)
	err := c.TCPConn.Close()
	if c.writes.Load() >= 2 {
		if p := c.client(); p != nil {
			_ = p.Signal(syscall.SIGCONT)
		}
	}
	return err
}

type resetAfterAlertListener struct {
	net.Listener
	client func() *os.Process
}

func (l resetAfterAlertListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &resetAfterAlertConn{TCPConn: c.(*net.TCPConn), client: l.client}, nil
}

// loadResetAfterAlertTest refuses the connection of the tool with a TLS
// alert after the handshake, and resets it. The tool counts the request as
// failed once and exits.
func loadResetAfterAlertTest(t *testing.T, load func(args ...string) *exec.Cmd) {
	var client atomic.Pointer[os.Process]
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Listener = resetAfterAlertListener{Listener: srv.Listener, client: client.Load}
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	defer srv.Close()
	cmd := load("-n", "1", "-c", "1", "--insecure", "--output-format", "json", srv.URL+"/")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	client.Store(cmd.Process)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Minute):
		_ = cmd.Process.Signal(syscall.SIGCONT)
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("load did not exit in 1 minute after a TLS alert and a reset\n%s", &stderr)
	}
	client.Store(nil)
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("load: exit %d\n%s%s", code, &stdout, &stderr)
	}
	j := parseLoadJSON(t, stdout.String())
	if j.responses() != 0 || sum(j.ErrorDistribution) != 1 {
		t.Errorf("%d responses and errors %v; want 0 responses and 1 error", j.responses(), j.ErrorDistribution)
	}
}
