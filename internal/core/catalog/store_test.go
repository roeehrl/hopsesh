package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestStoreSharedReadersAndScopedSnapshots(t *testing.T) {
	dir := t.TempDir()
	a, b := New(dir), New(dir)
	defer a.Close()
	defer b.Close()
	a.Update("scope", func(_ []byte) any { return map[string]string{"title": "new"} })
	var got map[string]string
	if !b.Read("scope", &got) || got["title"] != "new" {
		t.Fatalf("shared reader lost snapshot: %v", got)
	}
	if b.Read("another-account", &got) {
		t.Fatal("account scopes must not cross")
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprint(i)
			a.Save("a", path, "v1", map[string]int{"id": i})
			var s map[string]int
			if !b.Load("a", path, "v1", &s) || s["id"] != i {
				t.Errorf("shared summary %d: %v", i, s)
			}
		}()
	}
	wg.Wait()
	if st, err := os.Stat(filepath.Join(dir, "catalog", "sessions-v1.sqlite")); err != nil || runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		t.Fatalf("private file: %v %v", st, err)
	}
}

func TestSummaryInvalidationAndNegativeResult(t *testing.T) {
	s := New(t.TempDir())
	defer s.Close()
	s.Save("a", "one", "v1", nil)
	var got *string
	if !s.Load("a", "one", "v1", &got) || got != nil {
		t.Fatal("bookkeeping-only files should be cached too")
	}
	if s.Load("a", "one", "v2", &got) {
		t.Fatal("stale file reused")
	}
	s.InvalidatePath("one")
	if s.Load("a", "one", "v1", &got) {
		t.Fatal("same-size rewrite remained cached")
	}
}

func TestCloseReleasesCatalogFiles(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.Update("scope", func(_ []byte) any { return "saved" })
	var saved string
	if !s.Read("scope", &saved) || saved != "saved" {
		t.Fatal("catalog did not open")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Windows refuses to remove an open SQLite database. This checks lifecycle
	// ownership directly instead of relying only on test-directory cleanup.
	if err := os.RemoveAll(filepath.Join(dir, "catalog")); err != nil {
		t.Fatalf("closed catalog kept file handles open: %v", err)
	}
}

func TestCacheFailureIsOptional(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "catalog"), 0700)
	os.WriteFile(filepath.Join(dir, "catalog", "sessions-v1.sqlite"), []byte("broken"), 0600)
	s := New(dir)
	defer s.Close()
	var data any
	if s.Read("a", &data) {
		t.Fatal("corrupt cache accepted")
	}
	s.Save("a", "p", "sig", nil)
	s.Update("a", func(_ []byte) any { return map[string]string{"recovered": "yes"} })
	var recovered map[string]string
	if !s.Read("a", &recovered) || recovered["recovered"] != "yes" {
		t.Fatal("corrupt disposable cache was not rebuilt")
	}
}

func BenchmarkSavedCatalog10000(b *testing.B) {
	s := New(b.TempDir())
	defer s.Close()
	data := make([]map[string]any, 10000)
	for i := range data {
		data[i] = map[string]any{"title": fmt.Sprintf("Session %d", i), "agent": "claude", "path": fmt.Sprintf("/sessions/%d.jsonl", i)}
	}
	s.Update("a", func(_ []byte) any { return data })
	b.ResetTimer()
	for range b.N {
		var out []map[string]any
		if !s.Read("a", &out) {
			b.Fatal("cache miss")
		}
	}
}

func TestCollectorLeaseExpiresAndIndependentSourceUpdates(t *testing.T) {
	dir := t.TempDir()
	a, b := New(dir), New(dir)
	defer a.Close()
	defer b.Close()
	if !a.Claim("scan", "one") || b.Claim("scan", "two") {
		t.Fatal("collectors not coalesced")
	}
	_, _ = a.db.Exec("UPDATE leases SET expires=0")
	if !b.Claim("scan", "two") {
		t.Fatal("crashed collector blocked forever")
	}
	a.Release("scan", "one")
	if a.Claim("scan", "three") {
		t.Fatal("old collector released someone else's lease")
	}
	b.Release("scan", "two")
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Update("sources", func(old []byte) any {
				m := map[string]int{}
				_ = json.Unmarshal(old, &m)
				m[fmt.Sprint(i)] = i
				return m
			})
		}()
	}
	wg.Wait()
	var result map[string]int
	if !a.Read("sources", &result) || len(result) != 2 {
		t.Fatalf("independent source update lost: %v", result)
	}
}

func TestCollectorCacheFailureDoesNotBlockBrowsing(t *testing.T) {
	for _, closed := range []bool{false, true} {
		s := New(t.TempDir())
		if err := s.open(); err != nil {
			t.Fatal(err)
		}
		if closed {
			_ = s.Close()
		} else {
			_, err := s.db.Exec("PRAGMA query_only=ON")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
		}
		if !s.Claim("scope", "reader") {
			t.Fatal("unwritable cache blocks discovery")
		}
	}
}
