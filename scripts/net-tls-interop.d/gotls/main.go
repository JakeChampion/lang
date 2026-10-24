package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"time"
)

const bulkBytes = 16 << 20

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "server" && os.Args[1] != "client") {
		fmt.Fprintln(os.Stderr, "usage: gotls server|client")
		os.Exit(2)
	}
	if os.Args[1] == "server" {
		serve()
		return
	}
	client()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gotls:", err)
	os.Exit(1)
}

func rootsFrom(path string) *x509.CertPool {
	pem, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		fail(fmt.Errorf("%s holds no certificate", path))
	}
	return pool
}

func serve() {
	cert, err := tls.LoadX509KeyPair(os.Getenv("CERT"), os.Getenv("KEY"))
	if err != nil {
		fail(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello"))
	})
	mux.HandleFunc("/bulk", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(bulkBytes))
		_, _ = w.Write(bytes.Repeat([]byte("x"), bulkBytes))
	})
	base := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}}
	mtls := base.Clone()
	mtls.ClientAuth = tls.RequireAndVerifyClientCert
	mtls.ClientCAs = rootsFrom(os.Getenv("CLIENT_CA"))
	errs := make(chan error, 2)
	for _, l := range []struct {
		port string
		cfg  *tls.Config
	}{{os.Getenv("PORT"), base}, {os.Getenv("MTLS_PORT"), mtls}} {
		s := &http.Server{Addr: "127.0.0.1:" + l.port, Handler: mux, TLSConfig: l.cfg}
		go func() { errs <- s.ListenAndServeTLS("", "") }()
	}
	fail(<-errs)
}

func client() {
	roots := rootsFrom(os.Getenv("CA"))
	port, mtlsPort := os.Getenv("PORT"), os.Getenv("MTLS_PORT")
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost", ClientSessionCache: tls.NewLRUClientSessionCache(4), MinVersion: tls.VersionTLS13}}
	c := &http.Client{Transport: tr, Timeout: 60 * time.Second}
	var reused, resumed bool
	trace := &httptrace.ClientTrace{
		GotConn:          func(i httptrace.GotConnInfo) { reused = i.Reused },
		TLSHandshakeDone: func(cs tls.ConnectionState, err error) { resumed = cs.DidResume },
	}
	get := func(c *http.Client, url string) (string, []byte, error) {
		req, _ := http.NewRequest("GET", url, nil)
		resp, err := c.Do(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
		if err != nil {
			return "", nil, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return strconv.Itoa(resp.StatusCode), body, err
	}
	line := func(label string, c *http.Client, url string) {
		status, body, err := get(c, url)
		if err != nil {
			fmt.Printf("%s: error %v\n", label, err)
			return
		}
		fmt.Printf("%s: %s %s\n", label, status, strings.TrimSpace(string(body)))
	}
	base := "https://localhost:" + port
	line("get", c, base+"/hello")
	if _, _, err := get(c, base+"/hello"); err != nil {
		fmt.Printf("keepalive: error %v\n", err)
	} else if reused {
		fmt.Println("keepalive: reused")
	} else {
		fmt.Println("keepalive: a second connection")
	}
	tr.CloseIdleConnections()
	status, body, err := get(c, base+"/hello")
	switch {
	case err != nil:
		fmt.Printf("resume: error %v\n", err)
	case resumed:
		fmt.Printf("resume: resumed %s %s\n", status, body)
	default:
		fmt.Printf("resume: fresh %s %s\n", status, body)
	}
	started := time.Now()
	if _, body, err := get(c, base+"/bulk"); err != nil {
		fmt.Printf("bulk: error %v\n", err)
	} else {
		fmt.Printf("bulk: %d bytes %d ms\n", len(body), time.Since(started).Milliseconds())
	}
	ident, err := tls.LoadX509KeyPair(os.Getenv("CLIENT_CERT"), os.Getenv("CLIENT_KEY"))
	if err != nil {
		fail(err)
	}
	withCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost", Certificates: []tls.Certificate{ident}, MinVersion: tls.VersionTLS13}}, Timeout: 60 * time.Second}
	line("mtls", withCert, "https://localhost:"+mtlsPort+"/hello")
	bare := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS13}}, Timeout: 60 * time.Second}
	if _, _, err := get(bare, "https://localhost:"+mtlsPort+"/hello"); err != nil {
		fmt.Println("nocert: refused")
	} else {
		fmt.Println("nocert: accepted")
	}
}
