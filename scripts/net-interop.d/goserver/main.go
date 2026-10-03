// The Go net/http origin scripts/net-interop runs the Fern client against:
// the four targets every interop origin answers (see that script), with
// the server's defaults on PORT.
package main

import (
	"compress/gzip"
	"net"
	"net/http"
	"os"
	"strings"
)

// gzipText is the body /gzip codes; scripts/net-interop writes the same
// text for nginx and h2o.
var gzipText = strings.Repeat("hello gzip ", 30)

func main() {
	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})
	http.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hello", http.StatusFound)
	})
	http.HandleFunc("/port", func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(r.RemoteAddr)
		_, _ = w.Write([]byte(port))
	})
	// Coded as it is written, and flushed before the end so Go sends no
	// length: it goes out chunked, as nginx's and h2o's on-the-fly coding
	// does.
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
