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
		{From: `C:\Users\李华\proj`, To: "/home/lihua/proj", ToSep: "/"},
		{From: `C:\Users\李华`, To: "/home/lihua", ToSep: "/"},
	}, map[string]string{`C:\Users\李华\proj`: `C:\Users\LIHUA~1\proj`, `C:\Users\李华`: `C:\Users\LIHUA~1`})
	if len(ms) != 4 || ms[2].From != `C:\Users\LIHUA~1\proj` || ms[2].To != "/home/lihua/proj" || ms[2].ToSep != "/" {
		t.Fatalf("mappings: %+v", ms)
	}
	in := `{"cwd":"C:\\Users\\李华\\proj","t":"wrote C:\\Users\\LIHUA~1\\proj\\a.go and C:\\Users\\LIHUA~1\\AppData\\x"}` + "\n"
	var out bytes.Buffer
	if _, err := rewrite.JSONL(strings.NewReader(in), &out, rewrite.Options{Mappings: ms}); err != nil {
		t.Fatal(err)
	}
	want := `{"cwd":"/home/lihua/proj","t":"wrote /home/lihua/proj/a.go and /home/lihua/AppData/x"}` + "\n"
	if out.String() != want {
		t.Fatalf("got  %s\nwant %s", out.String(), want)
	}
}
