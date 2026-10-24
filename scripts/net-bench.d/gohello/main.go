package main

import (
	"compress/gzip"
	"net"
	"net/http"
	"os"
	"strings"
)

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
