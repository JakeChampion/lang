package e2ecompiler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostFractalCancellation(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("live cancellation gate runs natively on x86-64 Linux")
	}
	cli := buildSelfHostCLI(t)
	entry, err := filepath.Abs("../../../examples/fractal/server.fern")
	if err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, entry))
	cmd.Dir = filepath.Dir(entry)
	addr, _ := e2eharness.StartInheritedServer(t, cmd)
	client := &http.Client{Timeout: 10 * time.Second}
	base := "http://" + addr
	readStats := func() map[string]any {
		t.Helper()
		response, err := client.Get(base + "/api/stats")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var stats map[string]any
		if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
			t.Fatal(err)
		}
		return stats
	}
	var config map[string]any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get(base + "/api/config")
		if err == nil {
			defer response.Body.Close()
			if err := json.NewDecoder(response.Body).Decode(&config); err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if config == nil {
		t.Fatal("server did not start")
	}
	version := config["render_version"].(string)
	request, _ := http.NewRequest("GET", fmt.Sprintf("%s/tile/%s/42/1931133968718/2054079141036", base, version), nil)
	request.Header.Set("X-Fern-Session", "0123456789abcdef")
	request.Header.Set("X-Fern-Request", "1")
	type result struct {
		response *http.Response
		err      error
	}
	done := make(chan result, 1)
	go func() { response, err := client.Do(request); done <- result{response, err} }()
	active := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if readStats()["active"].(float64) > 0 {
			active = true
			break
		}
		select {
		case result := <-done:
			if result.response != nil {
				result.response.Body.Close()
			}
			t.Fatalf("render did not yield before completion: %v", result.err)
		default:
		}
	}
	if !active {
		t.Fatal("render did not start")
	}
	cancel, err := client.Post(base+"/cancel/0123456789abcdef/1", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel.Body.Close()
	if cancel.StatusCode != 204 {
		t.Fatalf("cancellation status %d", cancel.StatusCode)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		io.Copy(io.Discard, result.response.Body)
		result.response.Body.Close()
		if result.response.StatusCode != 499 {
			t.Fatalf("cancelled render status %d, want 499", result.response.StatusCode)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("render ignored cancellation")
	}
	stats := readStats()
	if stats["tiles"].(float64) != 0 || stats["active"].(float64) != 0 {
		t.Fatalf("cancelled work counted as a tile: %v", stats)
	}
}
