package agent

import (
	"context"
	"errors"
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

// A database session is cached by its key and the signature its module read; a new
// signature parses again, and no signature never caches.
func TestListingSummaryKey(t *testing.T) {
	memo := map[string]*Summary{}
	parses, found := 0, 0
	ctx := WithListingHooks(context.Background(), ListingHooks{
		Load:  func(k, s string) (*Summary, bool) { v, ok := memo[k+"|"+s]; return v, ok },
		Save:  func(k, s string, v *Summary) { memo[k+"|"+s] = v },
		Found: func(Summary) { found++ },
	})
	run := func(sig string) *Summary {
		s, err := ListingSummaryKey(ctx, nil, "/d/oc.db#ses_1", sig, "v1", func() (*Summary, error) {
			parses++
			return &Summary{Title: sig}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	run("ses_1|100|4|9")
	if got := run("ses_1|100|4|9"); parses != 1 || got.Title != "ses_1|100|4|9" {
		t.Fatalf("an unchanged session parsed %d times", parses)
	}
	run("ses_1|200|5|11")
	if parses != 2 {
		t.Fatal("a new signature must parse again")
	}
	run("")
	run("")
	if parses != 4 || found != 5 {
		t.Fatalf("without a signature: %d parses, %d found", parses, found)
	}
	if _, err := ListingSummaryKey(ctx, nil, "k", "s", "v2", func() (*Summary, error) { return nil, errors.New("bad row") }); err == nil {
		t.Fatal("a parse error is returned")
	}
	if len(memo) != 2 {
		t.Fatalf("cached %d entries", len(memo))
	}
}
