package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Previewer reads the end of a session's conversation for the app's preview. It is never
// called during a scan: only when the user selects a session.
type Previewer interface {
	Preview(ctx context.Context, h Host, in Install, s Summary, n int) (Preview, error)
}

// Preview is the end of a conversation: the last n messages, oldest first, with a line
// for the tool calls between a prompt and its reply and a marker where it was compacted.
type Preview struct {
	Items []PreviewItem `json:"items"`
	More  bool          `json:"more"`            // earlier messages exist
	First *PreviewItem  `json:"first,omitempty"` // the first real prompt, when found
}

// Messages counts the preview's user and agent messages (not its tool lines or markers).
func (p Preview) Messages() int {
	n := 0
	for _, it := range p.Items {
		if it.Role.message() {
			n++
		}
	}
	return n
}

// PreviewRole says what a preview item is.
type PreviewRole string

const (
	PreviewUser      PreviewRole = "user"
	PreviewAgent     PreviewRole = "agent"
	PreviewTools     PreviewRole = "tools"     // the tool calls of one turn (Tools)
	PreviewCompacted PreviewRole = "compacted" // the conversation was compacted here
)

func (r PreviewRole) message() bool { return r == PreviewUser || r == PreviewAgent }

// PreviewItem is one line of a preview.
type PreviewItem struct {
	Role  PreviewRole         `json:"role"`
	Text  string              `json:"text,omitempty"` // PreviewText of the message
	Time  time.Time           `json:"time"`
	Tools map[ir.ToolKind]int `json:"tools,omitempty"` // PreviewTools: calls by kind
}

// Renamer gives a session a new title in the agent's own data, the way the agent's own
// rename does, so the agent's session list shows it too.
type Renamer interface {
	Rename(ctx context.Context, h Host, in Install, s Summary, title string) error
}

// MaxTitle is the longest title Rename accepts, in characters.
const MaxTitle = 200

// CheckTitle validates a title given to Rename and returns it trimmed: it must not be
// empty, longer than MaxTitle, hold control characters or look like a mark title.
func CheckTitle(title string) (string, error) {
	t := strings.TrimSpace(title)
	switch {
	case t == "":
		return "", errors.New("a title cannot be empty")
	case utf8.RuneCountInString(t) > MaxTitle:
		return "", fmt.Errorf("a title can have at most %d characters", MaxTitle)
	case strings.IndexFunc(t, unicode.IsControl) >= 0:
		return "", errors.New("a title cannot contain control characters or line breaks")
	}
	for _, p := range MarkPrefixes() {
		if strings.HasPrefix(t, p) {
			return "", fmt.Errorf("a title cannot start with %q: hopsesh marks copies left behind that way", strings.TrimSpace(p))
		}
	}
	return t, nil
}

// BuildPreview turns a conversation's items, in order, into its preview: the last n
// messages and what lies between them. Modules pass what they read: user prompts, agent
// text (one item per block or record, as the agent wrote it), PreviewTools items and
// PreviewCompacted markers. In each turn the agent's message is its last run of text and
// its tool calls become one PreviewTools item; message text goes through PreviewText, and
// a message left empty is dropped. earlier says the items do not start at the beginning of
// the conversation (a tail window), so More is set even when every message is returned.
func BuildPreview(items []PreviewItem, n int, earlier bool) Preview {
	var (
		out      []PreviewItem
		tools    map[ir.ToolKind]int
		toolTime time.Time
		run      []string // the turn's latest run of agent text
		runTime  time.Time
		inRun    bool // the latest item was agent text
		runLast  bool // the run came after the turn's last tool call
	)
	flush := func() {
		var reply *PreviewItem
		if t := PreviewText(strings.Join(run, "\n\n")); t != "" {
			reply = &PreviewItem{Role: PreviewAgent, Text: t, Time: runTime}
		}
		var calls *PreviewItem
		if len(tools) > 0 {
			calls = &PreviewItem{Role: PreviewTools, Time: toolTime, Tools: tools}
		}
		if reply != nil && !runLast {
			out = append(out, *reply)
			reply = nil
		}
		if calls != nil {
			out = append(out, *calls)
		}
		if reply != nil {
			out = append(out, *reply)
		}
		tools, run, inRun, runLast = nil, nil, false, false
	}
	for _, it := range items {
		switch it.Role {
		case PreviewUser:
			flush()
			if t := PreviewText(it.Text); t != "" {
				out = append(out, PreviewItem{Role: PreviewUser, Text: t, Time: it.Time})
			}
		case PreviewCompacted:
			flush()
			if len(out) == 0 || out[len(out)-1].Role != PreviewCompacted {
				out = append(out, PreviewItem{Role: PreviewCompacted, Time: it.Time})
			}
		case PreviewTools:
			if tools == nil {
				tools = map[ir.ToolKind]int{}
			}
			for k, c := range it.Tools {
				tools[k] += c
			}
			toolTime, inRun, runLast = it.Time, false, false
		case PreviewAgent:
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			if !inRun {
				run, inRun, runLast = nil, true, true
			}
			run = append(run, it.Text)
			runTime = it.Time
		}
	}
	flush()

	start, count := 0, 0
	if n > 0 {
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role.message() {
				if count++; count == n {
					start = i
					break
				}
			}
		}
	} else {
		start = len(out)
	}
	p := Preview{Items: out[start:], More: earlier}
	if count < n {
		p.Items = out // every message: the lines before the first one belong too
	}
	for _, it := range out[:start] {
		if it.Role.message() {
			p.More = true
			break
		}
	}
	if p.Items == nil {
		p.Items = []PreviewItem{}
	}
	return p
}

// MaxPreviewText is the longest message text in a preview, in characters.
const MaxPreviewText = 1200

var (
	fenceRE    = regexp.MustCompile("^ {0,3}(```+|~~~+)")
	ansiRE     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	mdImageRE  = regexp.MustCompile(`!\[[^\]\n]*\]\([^)\n]*\)`)
	tagImageRE = regexp.MustCompile(`\[Image(?: #\d+)?(?:: [^\]\n]*)?\]|<img\b[^>\n]*>`)
)

// ImageMark stands for an image in preview text.
const ImageMark = "‹image›"

// PreviewText is a message's text as a preview shows it: without hopsesh's own note, code
// blocks and images replaced by short marks, whitespace collapsed inside paragraphs (one
// blank line between them), no control characters, and at most MaxPreviewText characters,
// cut at a word.
func PreviewText(s string) string {
	s = OwnText(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t' || r == '\r':
			return ' '
		case unicode.IsControl(r) || r == utf8.RuneError:
			return -1
		}
		return r
	}, s)

	var paras []string
	var para []string
	end := func() {
		if t := strings.Join(strings.Fields(strings.Join(para, " ")), " "); t != "" {
			paras = append(paras, t)
		}
		para = nil
	}
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if m := fenceRE.FindStringSubmatch(ln); m != nil {
			end()
			fence, n := m[1], 0
			for i++; i < len(lines); i++ {
				if c := strings.TrimSpace(lines[i]); strings.HasPrefix(c, fence) && strings.Trim(c, fence[:1]) == "" {
					break
				}
				n++
			}
			unit := "lines"
			if n == 1 {
				unit = "line"
			}
			paras = append(paras, fmt.Sprintf("‹code, %d %s›", n, unit))
			continue
		}
		if strings.TrimSpace(ln) == "" {
			end()
			continue
		}
		ln = mdImageRE.ReplaceAllString(ln, ImageMark)
		ln = tagImageRE.ReplaceAllString(ln, ImageMark)
		para = append(para, ln)
	}
	end()
	return cutWords(strings.Join(paras, "\n\n"), MaxPreviewText)
}

// cutWords shortens s to at most max characters, ending at a word with "…".
func cutWords(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	all := []rune(s)
	r := all[:max-1]
	cut := len(r)
	if !unicode.IsSpace(all[len(r)]) { // the cut falls inside a word: end at the one before
		for i := len(r) - 1; i > len(r)/2; i-- {
			if unicode.IsSpace(r[i]) {
				cut = i
				break
			}
		}
	}
	return strings.TrimRightFunc(string(r[:cut]), unicode.IsSpace) + "…"
}
