package move

import (
	"bytes"
	"strings"
	"testing"

	"github.com/roeehrl/hopsesh/internal/core/rewrite"
	"github.com/roeehrl/hopsesh/sdk/agent"
)

// A Windows session can name a folder by its long or its 8.3 short form; a move rewrites
// both to the same place.
func TestShortNamesMoveWithTheirFolder(t *testing.T) {
	ms := addShortNames([]agent.Mapping{
		{From: `C:\Users\דוד כהן\proj`, To: "/home/david/proj", ToSep: "/"},
		{From: `C:\Users\דוד כהן`, To: "/home/david", ToSep: "/"},
	}, map[string]string{`C:\Users\דוד כהן\proj`: `C:\Users\DAVIDC~1\proj`, `C:\Users\דוד כהן`: `C:\Users\DAVIDC~1`})
	if len(ms) != 4 || ms[2].From != `C:\Users\DAVIDC~1\proj` || ms[2].To != "/home/david/proj" || ms[2].ToSep != "/" {
		t.Fatalf("mappings: %+v", ms)
	}
	in := `{"cwd":"C:\\Users\\דוד כהן\\proj","t":"wrote C:\\Users\\DAVIDC~1\\proj\\a.go and C:\\Users\\DAVIDC~1\\AppData\\x"}` + "\n"
	var out bytes.Buffer
	if _, err := rewrite.JSONL(strings.NewReader(in), &out, rewrite.Options{Mappings: ms}); err != nil {
		t.Fatal(err)
	}
	want := `{"cwd":"/home/david/proj","t":"wrote /home/david/proj/a.go and /home/david/AppData/x"}` + "\n"
	if out.String() != want {
		t.Fatalf("got  %s\nwant %s", out.String(), want)
	}
}
