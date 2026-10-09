package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type listingFS struct{ FS }

func (listingFS) Stat(p string) (fs.FileInfo, error) { return os.Stat(p) }

type listingHost struct{ Host }

func (listingHost) FS() FS { return listingFS{} }

func TestListingSummaryCachesDependenciesAndRejectsChangingFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "session.jsonl")
	sidecar := filepath.Join(dir, "title.json")
	os.WriteFile(file, []byte("one"), 0600)
	memo := map[string]*Summary{}
	parses, published := 0, 0
	ctx := WithListingHooks(context.Background(), ListingHooks{Load: func(p, s string) (*Summary, bool) { v, ok := memo[p+s]; return v, ok }, Save: func(p, s string, v *Summary) { memo[p+s] = v }, Found: func(Summary) { published++ }})
	parse := func() (*Summary, error) { parses++; return &Summary{Title: "one"}, nil }
	run := func() {
		info, _ := os.Stat(file)
		if _, err := ListingSummary(ctx, listingHost{}, file, info, "v1", []string{sidecar}, parse); err != nil {
			t.Fatal(err)
		}
	}
	run()
	run()
	if parses != 1 || published != 2 {
		t.Fatalf("warm scan reparsed: %d / %d", parses, published)
	}
	os.WriteFile(sidecar, []byte("new title"), 0600)
	run()
	if parses != 2 {
		t.Fatal("new title sidecar failed to invalidate")
	}
	info, _ := os.Stat(file)
	changed := func() (*Summary, error) {
		parses++
		os.WriteFile(file, []byte("growing transcript"), 0600)
		return &Summary{}, nil
	}
	count := len(memo)
	_, err := ListingSummary(ctx, listingHost{}, file, info, "v2", nil, changed)
	if err != nil || len(memo) != count {
		t.Fatal("a changing parse was cached")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	info, _ = os.Stat(file)
	_, err = ListingSummary(cancelled, listingHost{}, file, info, "v3", nil, parse)
	if err == nil || published != 4 {
		t.Fatalf("cancelled parse published: %v %d", err, published)
	}
}

func TestListingSummaryDoesNotCacheAChangingDependency(t *testing.T) {
	dir := t.TempDir()
	file, dependency := filepath.Join(dir, "session.jsonl"), filepath.Join(dir, "titles.json")
	os.WriteFile(file, []byte("one"), 0600)
	saved := 0
	ctx := WithListingHooks(context.Background(), ListingHooks{Save: func(string, string, *Summary) { saved++ }})
	info, _ := os.Stat(file)
	_, err := ListingSummary(ctx, listingHost{}, file, info, "v1", []string{dependency}, func() (*Summary, error) {
		os.WriteFile(dependency, []byte("title appeared during parse"), 0600)
		return &Summary{Title: "outdated"}, nil
	})
	if err != nil || saved != 0 {
		t.Fatalf("changing dependency cached: %d %v", saved, err)
	}
}
