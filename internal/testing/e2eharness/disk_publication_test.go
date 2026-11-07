package e2eharness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDiskCachePublicationIsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", dir)
	payload := bytes.Repeat([]byte("compiled binary\n"), 65536)
	previous := bytes.Repeat([]byte("previous binary\n"), 65536)
	source := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(source, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "same-key.clibin"
	destination := filepath.Join(dir, name)
	if err := os.WriteFile(destination, previous, 0o755); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan struct{})
	readerDone := make(chan struct{})
	bad := make(chan error, 1)
	go func() {
		defer close(readerDone)
		<-start
		for {
			select {
			case <-done:
				return
			default:
			}
			got, err := os.ReadFile(destination)
			if err != nil || (!bytes.Equal(got, payload) && !bytes.Equal(got, previous)) {
				bad <- fmt.Errorf("a cache reader saw %d bytes, want %d complete bytes: %v", len(got), len(payload), err)
				return
			}
		}
	}()
	var writers sync.WaitGroup
	for range 16 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			for range 8 {
				publishToDiskCache(source, name)
			}
		}()
	}
	close(start)
	writers.Wait()
	close(done)
	<-readerDone
	select {
	case err := <-bad:
		t.Fatal(err)
	default:
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published binary: %d bytes, error %v", len(got), err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("published binary must be executable: %v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("cache must hold only the complete binary: %v, %v", entries, err)
	}
}

func TestDiskCachePublicationCleansFailedRename(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FERN_SELFHOST_BUILD_CACHE", dir)
	source := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(source, []byte("compiled binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := "blocked.clibin"
	if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
	publishToDiskCache(source, name)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != name || !entries[0].IsDir() {
		t.Fatalf("failed cache publication left temporary files: %v, %v", entries, err)
	}
}
