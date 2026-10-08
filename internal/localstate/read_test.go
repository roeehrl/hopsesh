package localstate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestBoundedStateReadRefusesLinksAndOversizedData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(p, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadPrivateFile(p, 7); err != nil || string(b) != "private" {
		t.Fatal(string(b), err)
	}
	if _, err := ReadPrivateFile(p, 6); err == nil {
		t.Fatal("oversized state read")
	}
	link := p + ".link"
	if err := os.Symlink(p, link); err == nil {
		if _, err := ReadPrivateFile(link, 7); err == nil {
			t.Fatal("state link followed")
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPrivateFile(p, 7); err == nil {
			t.Fatal("public state read")
		}
		if _, err := ReadOwnedFile(p, 7); err != nil {
			t.Fatal("authorized native read", err)
		}
	}
}

type mutatingReadFile struct {
	*os.File
	mutate func()
}

func (f *mutatingReadFile) Read(b []byte) (int, error) {
	n, err := f.File.Read(b)
	if f.mutate != nil {
		mutate := f.mutate
		f.mutate = nil
		mutate()
	}
	return n, err
}

func TestOwnedReadsRefuseMutationOfOpenedObject(t *testing.T) {
	for _, name := range []string{"append", "truncate", "same size rewrite", "replace pathname"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "native.jsonl")
			original := []byte(`{"content":"original"}` + "\n")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			oldTime := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, oldTime, oldTime); err != nil {
				t.Fatal(err)
			}
			opened, err := openOwnedRead(path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			f := &mutatingReadFile{File: opened, mutate: func() {
				body := original
				switch name {
				case "append":
					body = append(append([]byte{}, original...), original...)
				case "truncate":
					body = []byte("{}\n")
				case "same size rewrite":
					body = []byte(`{"content":"modified"}` + "\n")
				case "replace pathname":
					if runtime.GOOS == "windows" {
						// Windows intentionally denies replacing an owned open file.
						if err := os.Rename(path, path+".old"); err == nil {
							t.Fatal("opened native file was replaceable")
						}
						return
					}
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					body = []byte("{}\n")
				}
				if runtime.GOOS == "windows" {
					// The native read handle denies concurrent writes as well as
					// replacement; the stable snapshot must remain readable.
					if err := os.WriteFile(path, body, 0600); err == nil {
						t.Fatal("opened native file was writable")
					}
					return
				}
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, oldTime.Add(time.Minute), oldTime.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}}
			body, err := readStableFile(f, 4096)
			if name == "replace pathname" || runtime.GOOS == "windows" {
				if err != nil || string(body) != string(original) {
					t.Fatal("replacement changed the opened snapshot", string(body), err)
				}
			} else if err == nil || body != nil {
				t.Fatal("changing native file exposed an inconsistent snapshot", string(body), err)
			}
		})
	}
}
