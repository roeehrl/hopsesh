package rewrite

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// Mapping replaces one absolute path prefix with another (unescaped paths).
type Mapping struct {
	From string `json:"from"`
	To   string `json:"to"`
	// ToSep, when set ("/" or `\`), also converts the path separators that follow a
	// match (for moves between Windows and macOS/Linux). The rest of the path ends at the
	// first byte that cannot be part of a file name segment, such as a space or quote.
	ToSep string `json:"toSep,omitempty"`
}

// Options controls a transcript rewrite.
type Options struct {
	Mappings []Mapping
	// DropThinking removes thinking / redacted_thinking blocks entirely. Required when the
	// target machine is signed in to a different Anthropic account: those blocks carry
	// signatures bound to the original account, and removing only the signature fails.
	DropThinking bool
	// StripBridge drops bridge-session records so the copy does not reattach to the source
	// session's Remote Control link.
	StripBridge bool
	// AppendRelocated appends {"type":"relocated",...} the way Claude Code's /cd does, so
	// readers of the last 64 KB see the new project directory.
	SessionID    string
	RelocatedCWD string
	// Redact, if set, is applied to every rewritable string token (raw JSON bytes) after path
	// mapping; it returns the new bytes and how many secrets it replaced.
	Redact func(raw []byte) ([]byte, int)
}

// Stats reports what a rewrite changed.
type Stats struct {
	Lines            int            `json:"lines"`
	LinesChanged     int            `json:"linesChanged"`
	Replacements     map[string]int `json:"replacements"` // by Mapping.From
	DroppedBridge    int            `json:"droppedBridge"`
	DroppedThinking  int            `json:"droppedThinking"`
	Redactions       int            `json:"redactions"`
	AppendedRelocate bool           `json:"appendedRelocated"`
}

// maxLine bounds a single transcript line (Claude Code lines with large attachments can be
// several MB).
const maxLine = 256 << 20

// JSONL rewrites a transcript from r to w, line by line. Unchanged lines are copied byte
// for byte; the line structure (and the order of keys) is always preserved.
func JSONL(r io.Reader, w io.Writer, opt Options) (Stats, error) {
	st := Stats{Replacements: map[string]int{}}
	maps := compile(opt.Mappings, true)
	br := bufio.NewReaderSize(r, 1<<20)
	bw := bufio.NewWriterSize(w, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > maxLine {
			return st, errors.New("rewrite: transcript line exceeds 256 MB")
		}
		if len(line) > 0 {
			st.Lines++
			nl := bytes.HasSuffix(line, []byte{'\n'})
			body := bytes.TrimRight(line, "\r\n")
			out, keep := rewriteRecord(body, maps, opt, &st)
			if keep {
				if !bytes.Equal(out, body) {
					st.LinesChanged++
				}
				if _, werr := bw.Write(out); werr != nil {
					return st, werr
				}
				if nl {
					if werr := bw.WriteByte('\n'); werr != nil {
						return st, werr
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return st, err
		}
	}
	if opt.RelocatedCWD != "" {
		rec, _ := json.Marshal(struct {
			Type         string `json:"type"`
			SessionID    string `json:"sessionId"`
			RelocatedCWD string `json:"relocatedCwd"`
		}{"relocated", opt.SessionID, opt.RelocatedCWD})
		if _, err := bw.Write(append(rec, '\n')); err != nil {
			return st, err
		}
		st.AppendedRelocate = true
	}
	return st, bw.Flush()
}

// Text rewrites a plain-text file (spilled tool output) with unescaped path forms.
func Text(r io.Reader, w io.Writer, mappings []Mapping) (int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	counts := map[string]int{}
	out := replace(data, compile(mappings, false), counts)
	n := 0
	for _, c := range counts {
		n += c
	}
	_, err = w.Write(out)
	return n, err
}

func rewriteRecord(body []byte, maps []compiled, opt Options, st *Stats) ([]byte, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return body, true
	}
	if opt.StripBridge && bytes.Contains(body, []byte(`"bridge-session"`)) && recordType(body) == "bridge-session" {
		st.DroppedBridge++
		return nil, false
	}
	if opt.DropThinking && (bytes.Contains(body, []byte(`"thinking"`)) || bytes.Contains(body, []byte(`"redacted_thinking"`))) {
		if out, n, err := dropThinking(body); err == nil && n > 0 {
			body = out
			st.DroppedThinking += n
		}
	}
	return rewriteStrings(body, maps, opt.Redact, st), true
}

func recordType(body []byte) string {
	var v struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Type
}

// protectedKeys are object keys whose values (including nested containers) are never
// modified: signed thinking content, opaque redacted-thinking data, and identifiers.
var protectedKeys = map[string]bool{
	"thinking": true, "signature": true, "data": true,
	"uuid": true, "parentUuid": true, "sessionId": true, "leafUuid": true,
	"messageId": true, "promptId": true, "requestId": true, "logicalParentUuid": true,
}

type frame struct {
	obj       bool
	expectKey bool
	lastKey   string
}

// rewriteStrings walks one JSON value and applies path mappings (and redaction) inside
// string tokens, keys included, except under protected keys.
func rewriteStrings(b []byte, maps []compiled, redact func([]byte) ([]byte, int), st *Stats) []byte {
	var out bytes.Buffer
	out.Grow(len(b) + 64)
	var stack []frame
	protectDepth := -1 // stack depth at which a protected container started
	pendingProtected := false
	i := 0
	for i < len(b) {
		c := b[i]
		switch c {
		case '"':
			end := stringEnd(b, i)
			if end < 0 { // malformed; copy the rest untouched
				out.Write(b[i:])
				return out.Bytes()
			}
			tok := b[i : end+1]
			isKey := len(stack) > 0 && stack[len(stack)-1].obj && stack[len(stack)-1].expectKey
			if isKey {
				stack[len(stack)-1].lastKey = decodeKey(tok)
			}
			skip := protectDepth >= 0 || (!isKey && pendingProtected)
			if !skip {
				inner := tok[1 : len(tok)-1]
				counts := map[string]int{}
				ni := replace(inner, maps, counts)
				for k, v := range counts {
					st.Replacements[k] += v
				}
				if redact != nil {
					var n int
					ni, n = redact(ni)
					st.Redactions += n
				}
				out.WriteByte('"')
				out.Write(ni)
				out.WriteByte('"')
			} else {
				out.Write(tok)
			}
			if !isKey {
				pendingProtected = false
			}
			i = end + 1
			continue
		case '{', '[':
			if pendingProtected && protectDepth < 0 {
				protectDepth = len(stack)
			}
			pendingProtected = false
			stack = append(stack, frame{obj: c == '{', expectKey: c == '{'})
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if protectDepth >= 0 && len(stack) <= protectDepth {
				protectDepth = -1
			}
		case ':':
			if len(stack) > 0 && stack[len(stack)-1].obj {
				top := &stack[len(stack)-1]
				top.expectKey = false
				pendingProtected = protectDepth < 0 && protectedKeys[top.lastKey]
			}
		case ',':
			if len(stack) > 0 && stack[len(stack)-1].obj {
				stack[len(stack)-1].expectKey = true
			}
			pendingProtected = false
		default:
			// numbers, literals, whitespace: a scalar value under a protected key ends here
		}
		out.WriteByte(c)
		i++
	}
	return out.Bytes()
}

// stringEnd returns the index of the closing quote of the string starting at b[start].
func stringEnd(b []byte, start int) int {
	for j := start + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return -1
}

func decodeKey(tok []byte) string {
	if bytes.IndexByte(tok, '\\') < 0 {
		return string(tok[1 : len(tok)-1])
	}
	var s string
	_ = json.Unmarshal(tok, &s)
	return s
}

type compiled struct {
	key            string // Mapping.From, for stats
	from, to       []byte
	fromSep, toSep []byte // separator conversion after a match (nil: none)
	escaped        bool   // operating on JSON-escaped string content
}

// compile prepares mappings, longest first. escaped selects the JSON-escaped forms used
// inside transcript string tokens (Windows backslashes become \\).
func compile(ms []Mapping, escaped bool) []compiled {
	out := make([]compiled, 0, len(ms))
	for _, m := range ms {
		if m.From == "" || m.From == m.To {
			continue
		}
		from, to := []byte(m.From), []byte(m.To)
		if escaped {
			from, to = jsonEscape(m.From), jsonEscape(m.To)
		}
		c := compiled{key: m.From, from: from, to: to, escaped: escaped}
		if m.ToSep == "/" || m.ToSep == `\` {
			fromSep := `\`
			if m.ToSep == `\` {
				fromSep = "/"
			}
			c.fromSep, c.toSep = []byte(fromSep), []byte(m.ToSep)
			if escaped {
				c.fromSep, c.toSep = jsonEscape(fromSep), jsonEscape(m.ToSep)
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].from) > len(out[j].from) })
	return out
}

func jsonEscape(s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	return b[1 : len(b)-1]
}

// replace performs a single left-to-right pass, trying the longest mapping at each
// position, so a replacement is never itself replaced again. A match must sit on path
// boundaries: the byte before it must not continue a path segment, nor the byte after.
func replace(s []byte, maps []compiled, counts map[string]int) []byte {
	if len(maps) == 0 {
		return s
	}
	var out []byte
	last := 0
	escaped := maps[0].escaped
	for i := 0; i < len(s); i++ {
		if i > 0 && segmentByte(s[i-1]) && !(escaped && afterEscape(s, i)) {
			continue
		}
		if escaped && i > 0 && oddBackslashes(s, i-1) {
			continue // s[i] is the second half of an escape such as \/ (an escaped slash)
		}
		for _, m := range maps {
			if !bytes.HasPrefix(s[i:], m.from) {
				continue
			}
			end := i + len(m.from)
			if end < len(s) && segmentByte(s[end]) {
				continue
			}
			if out == nil {
				out = make([]byte, 0, len(s)+32)
			}
			out = append(out, s[last:i]...)
			out = append(out, m.to...)
			counts[m.key]++
			if m.fromSep != nil {
				// Convert separators in the rest of the path: separators and segment
				// bytes only, so the path ends at a space, quote or JSON escape.
				for end < len(s) {
					if bytes.HasPrefix(s[end:], m.fromSep) {
						out = append(out, m.toSep...)
						end += len(m.fromSep)
						continue
					}
					if !segmentByte(s[end]) {
						break
					}
					out = append(out, s[end])
					end++
				}
			}
			last = end
			i = end - 1
			break
		}
	}
	if out == nil {
		return s
	}
	return append(out, s[last:]...)
}

// afterEscape reports whether position i directly follows a complete JSON escape sequence
// (\n, \t, \r, \b, \f or \uXXXX), e.g. a path at the start of a new line inside
// tool output. An escaped backslash (\\) followed by a letter is not an escape.
func afterEscape(s []byte, i int) bool {
	if i >= 2 && strings.IndexByte("ntrbf", s[i-1]) >= 0 && oddBackslashes(s, i-2) {
		return true
	}
	if i >= 6 && s[i-5] == 'u' && isHex(s[i-4]) && isHex(s[i-3]) && isHex(s[i-2]) && isHex(s[i-1]) && oddBackslashes(s, i-6) {
		return true
	}
	return false
}

// oddBackslashes reports whether the run of backslashes ending at j has odd length (so the
// backslash at j starts an escape rather than being an escaped backslash).
func oddBackslashes(s []byte, j int) bool {
	n := 0
	for ; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// segmentByte reports whether c continues a path segment name.
func segmentByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c >= 0x80
}
