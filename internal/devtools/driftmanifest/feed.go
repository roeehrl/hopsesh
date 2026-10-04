package main

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// feedDoc reads both RSS 2.0 (channel/item) and Atom (entry).
type feedDoc struct {
	Items   []feedItem `xml:"channel>item"`
	Entries []feedItem `xml:"entry"`
}

type feedItem struct {
	Title       string     `xml:"title"`
	Links       []feedLink `xml:"link"`
	PubDate     string     `xml:"pubDate"`
	Published   string     `xml:"published"`
	Updated     string     `xml:"updated"`
	Description string     `xml:"description"`
	Encoded     string     `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Content     string     `xml:"content"`
	Summary     string     `xml:"summary"`
}

// feedLink is an RSS link (its text) or an Atom link (its href).
type feedLink struct {
	Text string `xml:",chardata"`
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// maxItemText bounds each item: the review greps these files, and a release note's first
// screen says what it is about.
const maxItemText = 3000

// printFeed writes the items of the RSS or Atom file at path published after since, newest
// first as the feed lists them, as Markdown. Items without a readable date are counted, not
// printed: they would come back every week.
func printFeed(w io.Writer, path, since string) error {
	after, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return fmt.Errorf("since: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var d feedDoc
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&d); err != nil {
		return err
	}
	items := append(d.Items, d.Entries...)
	n, undated := 0, 0
	for _, it := range items {
		when, ok := it.date()
		if !ok {
			undated++
			continue
		}
		if !when.After(after) {
			continue
		}
		n++
		fmt.Fprintf(w, "### %s\n\n%s · %s\n\n%s\n\n", oneLine(it.Title), when.UTC().Format(time.RFC3339), it.link(), it.text())
	}
	fmt.Fprintf(w, "<!-- %d of %d items since %s", n, len(items), since)
	if undated > 0 {
		fmt.Fprintf(w, "; %d without a readable date", undated)
	}
	fmt.Fprintln(w, " -->")
	return nil
}

var dateLayouts = []string{time.RFC3339, time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2006-01-02"}

func (it feedItem) date() (time.Time, bool) {
	for _, s := range []string{it.Published, it.PubDate, it.Updated} {
		s = strings.TrimSpace(s)
		for _, l := range dateLayouts {
			if t, err := time.Parse(l, s); err == nil && s != "" {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func (it feedItem) link() string {
	for _, l := range it.Links {
		if l.Href != "" && (l.Rel == "" || l.Rel == "alternate") {
			return l.Href
		}
		if t := strings.TrimSpace(l.Text); t != "" {
			return t
		}
	}
	return ""
}

var (
	inlineRE = regexp.MustCompile(`(?i)</?(a|abbr|b|code|em|i|kbd|mark|s|small|span|strong|sub|sup|u)\b[^>]*>`)
	tagRE    = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRE  = regexp.MustCompile(`[ \t\r\f\v\x{a0}]+`)
	blankRE  = regexp.MustCompile(`\n\s*\n+`)
)

// text is the item's longest body as plain text, cut at maxItemText: inline tags are
// dropped, other tags break the line.
func (it feedItem) text() string {
	s := it.Description
	for _, c := range []string{it.Encoded, it.Content, it.Summary} {
		if len(c) > len(s) {
			s = c
		}
	}
	s = html.UnescapeString(tagRE.ReplaceAllString(inlineRE.ReplaceAllString(s, ""), "\n"))
	s = blankRE.ReplaceAllString(spaceRE.ReplaceAllString(s, " "), "\n\n")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxItemText {
		s = string(r[:maxItemText]) + " …"
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
