// Package skill renders the hopsesh skill: one set of files every agent reads the same.
package skill

import (
	"bytes"
	"embed"
	"strings"
	"text/template"
)

//go:embed files/*.tmpl
var templates embed.FS

// Params fill the templates.
type Params struct {
	Bin        string // how agents run hopsesh: "hopsesh" or an absolute path
	Version    string
	AgentNames string // "Claude Code and Codex"
	AgentIDs   string // "claude|codex"
}

// NewParams joins the supported agents' names and ids.
func NewParams(bin, version string, names, ids []string) Params {
	p := Params{Bin: bin, Version: version, AgentIDs: strings.Join(ids, "|")}
	switch len(names) {
	case 0:
	case 1:
		p.AgentNames = names[0]
	default:
		p.AgentNames = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	if p.Bin == "" {
		p.Bin = "hopsesh"
	}
	return p
}

// Render returns the skill's files (name → content).
func Render(p Params) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range []string{"SKILL.md", "reference.md"} {
		src, err := templates.ReadFile("files/" + name + ".tmpl")
		if err != nil {
			return nil, err
		}
		t, err := template.New(name).Parse(string(src))
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		if err := t.Execute(&b, p); err != nil {
			return nil, err
		}
		out[name] = bytes.ReplaceAll(b.Bytes(), []byte("\r\n"), []byte("\n"))
	}
	return out, nil
}
