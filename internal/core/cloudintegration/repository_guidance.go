package cloudintegration

import (
	"bytes"
	"errors"
	"os"
	"unicode/utf8"
)

const codexGuidanceBegin = "<!-- hopsesh:codex-cloud-start -->"
const codexGuidanceEnd = "<!-- /hopsesh:codex-cloud-start -->"

// Only this marked block belongs to Hopsesh. Repository instructions outside it
// are preserved byte-for-byte and never exposed through the setup review bridge.
func (p *RepositorySetup) codexGuidance(root *os.Root, record *startupOwnership) error {
	override, err := readStartup(root, "AGENTS.override.md")
	if err != nil {
		return err
	}
	if override != nil {
		return errors.New("AGENTS.override.md takes precedence over AGENTS.md; review repository startup guidance there before installing")
	}
	// A new override between review and apply also invalidates the plan.
	p.before["AGENTS.override.md"] = nil
	before, err := readStartup(root, "AGENTS.md")
	if err != nil {
		return err
	}
	if !utf8.Valid(before) {
		return errors.New("repository AGENTS.md must be UTF-8 text")
	}
	block := []byte(codexGuidanceBegin + "\n## Hopsesh in Codex Cloud\n\n" +
		"For a normal Codex Cloud task, read `.hopsesh/codex-start.md` before repository work and follow its disconnected startup instructions. " +
		"Skip this in environment setup/edit conversations, local sessions and other agents. If the execution surface or actual task identity is unavailable, report that limitation and do not prepare an incarnation. " +
		"These instructions grant no network connection, enrollment, transcript export, receiver, or permission changes.\n" + codexGuidanceEnd)
	begin, end := bytes.Index(before, []byte(codexGuidanceBegin)), bytes.Index(before, []byte(codexGuidanceEnd))
	var body []byte
	if begin == -1 && end == -1 {
		if record.Guidance != "" {
			return errors.New("hopsesh startup guidance was removed; review repository instructions before reinstalling")
		}
		body = bytes.Clone(before)
		if len(body) != 0 {
			body = append(body, '\n', '\n')
		}
		body = append(body, block...)
		body = append(body, '\n')
	} else {
		if begin < 0 || end < begin || bytes.Count(before, []byte(codexGuidanceBegin)) != 1 || bytes.Count(before, []byte(codexGuidanceEnd)) != 1 {
			return errors.New("hopsesh startup guidance markers are ambiguous; review AGENTS.md before installing")
		}
		end += len(codexGuidanceEnd)
		old := before[begin:end]
		if !bytes.Equal(old, block) && record.Guidance != startupHash(old) {
			return errors.New("hopsesh startup guidance was modified or is not owned by Hopsesh; review AGENTS.md before installing")
		}
		body = append(bytes.Clone(before[:begin]), block...)
		body = append(body, before[end:]...)
	}
	// Codex's default instruction budget is 32 KiB across the guidance chain.
	// Refuse a root file that alone exceeds it; other guidance may reduce what
	// fits, so actual delivery still needs verification in a fresh cloud task.
	if len(body) > 32*1024 {
		return errors.New("AGENTS.md with startup guidance exceeds the default 32 KiB instruction budget; shorten repository guidance before installing")
	}
	p.before["AGENTS.md"], p.files["AGENTS.md"] = before, body
	record.Guidance = startupHash(block)
	return nil
}
