package fakeopencode

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// OpenCode's server decodes an import with its schemas (packages/schema/src: session.ts,
// session-message.ts, prompt.ts, tool.ts and what they use): a member of the wrong kind,
// a required one missing, or an unknown type or status is a Bad Request, and members a
// schema does not declare are dropped. decodeTransfer does the same, and also says which
// members it dropped. Records (metadata, native, tool inputs) are kept as they are.

// A shape checks a value against one schema and returns it as the schema decodes it.
type shape func(d *decoder, v *value, at string) (*value, error)

type decoder struct {
	dropped []string // members left out, as paths
}

func (d *decoder) bad(at, f string, a ...any) error {
	return &badRequest{msg: strings.TrimPrefix(at, "$.") + ": " + fmt.Sprintf(f, a...)}
}

func describe(v *value) string {
	switch {
	case v == nil:
		return "missing"
	case v.kind == 's':
		return strconv.Quote(v.s)
	case v.kind == 'o':
		return "an object"
	case v.kind == 'a':
		return "a list"
	}
	return v.s
}

func isNumber(v *value) (float64, bool) {
	if v == nil || v.kind != 'l' {
		return 0, false
	}
	f, err := strconv.ParseFloat(v.s, 64)
	return f, err == nil && !math.IsInf(f, 0) && !math.IsNaN(f)
}

var (
	tString shape = func(d *decoder, v *value, at string) (*value, error) {
		if v == nil || v.kind != 's' {
			return nil, d.bad(at, "expected a string, got %s", describe(v))
		}
		return v, nil
	}
	tFinite shape = func(d *decoder, v *value, at string) (*value, error) {
		if _, ok := isNumber(v); !ok {
			return nil, d.bad(at, "expected a number, got %s", describe(v))
		}
		return v, nil
	}
	// tMillis is DateTimeUtcFromMillis: a number, kept as a JavaScript Date keeps it
	// (whole milliseconds).
	tMillis shape = func(d *decoder, v *value, at string) (*value, error) {
		f, ok := isNumber(v)
		if !ok {
			return nil, d.bad(at, "expected a time in milliseconds, got %s", describe(v))
		}
		if f != math.Trunc(f) {
			return lit(jsNumber(math.Trunc(f))), nil
		}
		return v, nil
	}
	tBool shape = func(d *decoder, v *value, at string) (*value, error) {
		if v == nil || v.kind != 'l' || (v.s != "true" && v.s != "false") {
			return nil, d.bad(at, "expected true or false, got %s", describe(v))
		}
		return v, nil
	}
	// tRecord is a record of anything (metadata, native, a tool's input): kept as it is.
	tRecord shape = func(d *decoder, v *value, at string) (*value, error) {
		if !v.isObj() {
			return nil, d.bad(at, "expected an object, got %s", describe(v))
		}
		return v, nil
	}
)

// tInt is a whole number in [lo, hi].
func tInt(lo, hi float64) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		f, ok := isNumber(v)
		if !ok || f != math.Trunc(f) || f < lo || f > hi {
			return nil, d.bad(at, "expected a whole number from %v to %v, got %s", lo, hi, describe(v))
		}
		return v, nil
	}
}

var (
	tNonNegative = tInt(0, math.MaxFloat64)
	tPositive    = tInt(1, math.MaxFloat64)
)

// tPrefixed is a string starting with prefix (an id).
func tPrefixed(prefix string) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		if v == nil || v.kind != 's' || !strings.HasPrefix(v.s, prefix) {
			return nil, d.bad(at, "expected a string starting with %q, got %s", prefix, describe(v))
		}
		return v, nil
	}
}

func tPattern(re *regexp.Regexp, what string) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		if v == nil || v.kind != 's' || !re.MatchString(v.s) {
			return nil, d.bad(at, "expected %s, got %s", what, describe(v))
		}
		return v, nil
	}
}

func tLiterals(xs ...string) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		if v != nil && v.kind == 's' {
			for _, x := range xs {
				if v.s == x {
					return v, nil
				}
			}
		}
		return nil, d.bad(at, "expected one of %s, got %s", strings.Join(xs, ", "), describe(v))
	}
}

func tTrue(d *decoder, v *value, at string) (*value, error) {
	if v == nil || v.kind != 'l' || v.s != "true" {
		return nil, d.bad(at, "expected true, got %s", describe(v))
	}
	return v, nil
}

func tArray(item shape, nonEmpty bool) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		if v == nil || v.kind != 'a' {
			return nil, d.bad(at, "expected a list, got %s", describe(v))
		}
		if nonEmpty && len(v.vals) == 0 {
			return nil, d.bad(at, "expected at least one item")
		}
		out := arr()
		for i, x := range v.vals {
			y, err := item(d, x, fmt.Sprintf("%s[%d]", at, i))
			if err != nil {
				return nil, err
			}
			out.vals = append(out.vals, y)
		}
		return out, nil
	}
}

// A field is one member of a struct.
type field struct {
	name     string
	s        shape
	optional bool
}

func req(name string, s shape) field { return field{name, s, false} }
func opt(name string, s shape) field { return field{name, s, true} }

// tStruct is a struct: its members in the order it declares them, the others dropped.
func tStruct(fields ...field) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		if !v.isObj() {
			return nil, d.bad(at, "expected an object, got %s", describe(v))
		}
		out := obj()
		known := map[string]bool{}
		for _, f := range fields {
			known[f.name] = true
			x := v.get(f.name)
			where := at + "." + f.name
			if x == nil {
				if !f.optional {
					return nil, d.bad(where, "is required")
				}
				continue
			}
			y, err := f.s(d, x, where)
			if err != nil {
				return nil, err
			}
			out.set(f.name, y)
		}
		for _, k := range v.keys {
			if !known[k] {
				d.dropped = append(d.dropped, at+"."+k)
			}
		}
		return out, nil
	}
}

// tTagged is a union told apart by the string member tag.
func tTagged(tag string, cases map[string]shape) shape {
	return func(d *decoder, v *value, at string) (*value, error) {
		if !v.isObj() {
			return nil, d.bad(at, "expected an object, got %s", describe(v))
		}
		s, ok := cases[v.text(tag)]
		if !ok {
			names := make([]string, 0, len(cases))
			for k := range cases {
				names = append(names, k)
			}
			slices.Sort(names)
			return nil, d.bad(at+"."+tag, "expected one of %s, got %s", strings.Join(names, ", "), describe(v.get(tag)))
		}
		return s(d, v, at)
	}
}

// OpenCode 2.3.0's schemas, as far as an import reaches.
var (
	tModelRef    = tStruct(req("id", tString), req("providerID", tString), opt("variant", tString))
	tLocationRef = tStruct(req("directory", tString), opt("workspaceID", tString))
	tTokens      = tStruct(req("input", tFinite), req("output", tFinite), req("reasoning", tFinite),
		req("cache", tStruct(req("read", tFinite), req("write", tFinite))))
	tError = tStruct(req("type", tString), req("message", tString), opt("status", tInt(100, 599)),
		opt("response", tStruct(req("body", tString))), opt("retryAfterMs", tNonNegative))
	tMention = tStruct(req("start", tFinite), req("end", tFinite), req("text", tString))
	tBase64  = tPattern(regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$`), "base64 data")
	// A prompt's file carries its data: a stored file's media: ref is not accepted.
	tFileAttachment = tStruct(req("data", tBase64), req("mime", tString),
		req("source", tTagged("type", map[string]shape{
			"inline": tStruct(req("type", tString)),
			"uri":    tStruct(req("type", tString), req("uri", tString)),
		})),
		opt("name", tString), opt("description", tString), opt("mention", tMention))
	tToolContent = tArray(tTagged("type", map[string]shape{
		"text": tStruct(req("type", tString), req("text", tString)),
		"file": tStruct(req("type", tString), req("uri", tString), req("mime", tString), opt("name", tString)),
	}), true)
	tToolState = tTagged("status", map[string]shape{
		"streaming": tStruct(req("status", tString), req("input", tString)),
		"running":   tStruct(req("status", tString), req("input", tRecord), req("metadata", tRecord)),
		"completed": tStruct(req("status", tString), req("input", tRecord), req("content", tToolContent), opt("metadata", tRecord)),
		"error": tStruct(req("status", tString), req("input", tRecord), req("error", tError), opt("content", tToolContent),
			opt("metadata", tRecord)),
	})
	tAssistantContent = tTagged("type", map[string]shape{
		"text": tStruct(req("type", tString), req("text", tString), opt("native", tRecord)),
		"reasoning": tStruct(req("type", tString), req("text", tString), opt("native", tRecord),
			opt("time", tStruct(req("created", tMillis), opt("completed", tMillis)))),
		"tool": tStruct(req("type", tString), req("id", tString), req("name", tString), opt("executed", tBool),
			opt("native", tRecord), req("state", tToolState),
			req("time", tStruct(req("created", tMillis), opt("ran", tMillis), opt("completed", tMillis)))),
	})
)

// message is a message type's struct: Base (id, metadata, time) and its own members.
func message(time shape, fields ...field) shape {
	if time == nil {
		time = tStruct(req("created", tMillis))
	}
	base := []field{req("id", tPrefixed("msg_")), opt("metadata", tRecord), req("time", time), req("type", tString)}
	return tStruct(append(base, fields...)...)
}

var tMessage = tTagged("type", map[string]shape{
	"agent-switched": message(nil, req("agent", tString), opt("previous", tString)),
	"model-switched": message(nil, req("model", tModelRef), opt("previous", tModelRef)),
	"location-switched": message(nil, req("location", tLocationRef), opt("projectID", tString), opt("subpath", tString),
		opt("previous", tStruct(req("location", tLocationRef), opt("projectID", tString), opt("subpath", tString)))),
	"user": message(nil, req("text", tString), opt("files", tArray(tFileAttachment, false)),
		opt("agents", tArray(tStruct(req("name", tString), opt("mention", tMention)), false)),
		opt("skills", tArray(tStruct(req("id", tString), req("name", tString), opt("text", tString), opt("mention", tMention)), false))),
	"synthetic": message(nil, req("text", tString), opt("description", tString)),
	"system":    message(nil, req("text", tString), opt("description", tString)),
	"skill":     message(nil, req("skill", tString), req("name", tString), req("text", tString)),
	"shell": message(tStruct(req("created", tMillis), opt("completed", tMillis)),
		req("shellID", tPrefixed("sh_")), req("command", tString), req("status", tLiterals("running", "exited", "timeout", "killed")),
		opt("exit", tFinite), opt("output", tStruct(req("output", tString), req("cursor", tNonNegative), req("size", tNonNegative),
			req("truncated", tBool)))),
	"assistant": message(tStruct(req("created", tMillis), opt("firstToken", tMillis), opt("streamed", tMillis), opt("completed", tMillis)),
		req("agent", tString), req("model", tModelRef), req("content", tArray(tAssistantContent, false)),
		opt("snapshot", tStruct(opt("start", tString), opt("end", tString), opt("files", tArray(tString, false)))),
		opt("finish", tLiterals("stop", "length", "tool-calls", "content-filter", "error", "unknown")),
		opt("rawFinish", tString), opt("native", tRecord), opt("cost", tFinite), opt("tokens", tTokens), opt("error", tError),
		opt("retry", tStruct(req("attempt", tPositive), req("at", tMillis), req("error", tError)))),
	"compaction": tTagged("status", map[string]shape{
		"running": message(nil, req("status", tString), req("reason", tLiterals("auto", "manual")), req("summary", tString)),
		"completed": message(nil, req("status", tString), req("reason", tLiterals("auto", "manual")), opt("model", tModelRef),
			opt("native", tRecord), req("summary", tString), opt("cost", tFinite), opt("tokens", tTokens)),
		"failed": message(nil, req("status", tString), req("reason", tLiterals("auto", "manual")), req("error", tError),
			opt("cost", tFinite), opt("tokens", tTokens)),
	}),
	"idle": message(nil, req("outcome", tLiterals("succeeded", "failed", "interrupted")), opt("error", tError)),
})

var (
	tPermissions = tArray(tStruct(req("action", tString), req("resource", tString), req("effect", tLiterals("allow", "deny", "ask"))), false)
	tInfo        = tStruct(
		req("id", tPrefixed("ses")), opt("parentID", tPrefixed("ses")),
		opt("fork", tStruct(req("sessionID", tPrefixed("ses")), req("boundary", tStruct(
			req("type", tLiterals("before", "through")), req("messageID", tPrefixed("msg_")))))),
		req("projectID", tString), opt("agent", tString), opt("model", tModelRef), req("cost", tFinite), req("tokens", tTokens),
		opt("outcome", tLiterals("succeeded", "failed", "interrupted")),
		req("time", tStruct(req("created", tMillis), req("updated", tMillis), opt("busy", tMillis), opt("idle", tMillis),
			opt("viewed", tMillis), opt("archived", tMillis))),
		opt("title", tString), req("location", tLocationRef), opt("subpath", tString), opt("metadata", tRecord),
		opt("permissions", tPermissions),
		opt("revert", tStruct(req("messageID", tPrefixed("msg_")), opt("partID", tString), opt("snapshot", tString),
			opt("files", tArray(tStruct(req("file", tString), req("patch", tString), req("additions", tNonNegative),
				req("deletions", tNonNegative), req("status", tLiterals("added", "deleted", "modified")), opt("more", tTrue)), false)))),
	)
	tTransfer = tStruct(req("info", tInfo), req("messages", tArray(tMessage, false)))
)

// decodeTransfer decodes a transfer as OpenCode 2.3.0's import does: the transfer as it
// keeps it, and the members it dropped (paths like "messages[0].extra").
func decodeTransfer(doc *value) (*value, []string, error) {
	d := &decoder{}
	out, err := tTransfer(d, doc, "$")
	if err != nil {
		return nil, nil, err
	}
	for i, p := range d.dropped {
		d.dropped[i] = strings.TrimPrefix(p, "$.")
	}
	return out, d.dropped, nil
}

// CheckTransfer reports whether OpenCode 2.3.0 would import the transfer b (an error
// saying why not), and the members its import would drop.
func CheckTransfer(b []byte) (dropped []string, err error) {
	doc, err := parseJSON(b)
	if err != nil {
		return nil, err
	}
	_, dropped, err = decodeTransfer(doc)
	return dropped, err
}

// decodePermissions decodes a PATCH's permissions; a refusal reads as the server's
// ("Missing key\n  at [\"permissions\"][0][\"resource\"]").
func decodePermissions(v *value) (*value, error) {
	out, err := tPermissions(&decoder{}, v, "permissions")
	if err != nil {
		at, msg, _ := strings.Cut(err.Error(), ": ")
		if msg == "is required" {
			msg = "Missing key"
		}
		path := regexp.MustCompile(`[A-Za-z]+`).ReplaceAllStringFunc(at, func(k string) string { return `["` + k + `"]` })
		return nil, &badRequest{msg + "\n  at " + strings.ReplaceAll(path, ".", ""), "Payload"}
	}
	return out, nil
}
