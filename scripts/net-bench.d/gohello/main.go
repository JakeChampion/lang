// The Go net/http server the net-nightly lane runs, with the server's
// defaults, on PORT. scripts/net-bench loads `/`, the same static body as
// the Fern hello server, so the two columns differ only in the server;
// scripts/net-interop also reads /redirect, /port and /gzip, the targets
// every interop origin answers.
package main

import (
	"compress/gzip"
	"net"
	"net/http"
	"os"
	"strings"
)

// The body /gzip codes; scripts/net-lib gives h2o the same text.
var gzipText = strings.Repeat("hello gzip ", 30)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("hello"))
	})
	http.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hello", http.StatusFound)
	})
	http.HandleFunc("/port", func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(r.RemoteAddr)
		_, _ = w.Write([]byte(port))
	})
	// Flushed before the end, so Go sends no length and the body goes out
	// chunked, as nginx's and h2o's on-the-fly coding does.
	http.HandleFunc("/gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			_, _ = w.Write([]byte(gzipText))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		_, _ = z.Write([]byte(gzipText))
		_ = z.Flush()
		w.(http.Flusher).Flush()
		_ = z.Close()
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := http.ListenAndServe("127.0.0.1:"+port, nil); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
