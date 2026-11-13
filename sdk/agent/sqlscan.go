package agent

import (
	"fmt"
	"slices"
	"strings"
)

// CheckSQLQuery checks a query's text before it reaches a database: one SELECT (or WITH,
// VALUES) that writes nothing, reads only tables in allow (and the query's own WITH
// names), and never mentions a table in deny. Names compare without regard to case. It is
// a guard on the text; a Host also refuses reads below it where it can (SQLTableLimiter).
func CheckSQLQuery(q string, allow, deny []string) error {
	toks, err := sqlTokens(q)
	if err != nil {
		return err
	}
	if len(toks) == 0 {
		return deniedSQL("an empty query")
	}
	if !toks[0].is("SELECT", "WITH", "VALUES") {
		return deniedSQL("only SELECT queries are run")
	}
	for i, t := range toks {
		if t.text == ";" {
			if i != len(toks)-1 {
				return deniedSQL("one query at a time")
			}
			continue
		}
		if t.kind == tokIdent && !t.quoted && sqlWriteWords[strings.ToUpper(t.text)] {
			if strings.EqualFold(t.text, "REPLACE") && i+1 < len(toks) && toks[i+1].text == "(" {
				continue // the replace() function
			}
			return deniedSQL(strings.ToUpper(t.text) + " is not run")
		}
		if t.kind == tokIdent && sqlBannedFuncs[strings.ToLower(t.text)] {
			return deniedSQL(t.text + "() is not run")
		}
		if t.kind == tokIdent && containsFold(deny, t.text) {
			return deniedSQL("the table " + t.text + " is never read")
		}
	}
	ctes := sqlCTENames(toks)
	for i, t := range toks {
		if !t.is("FROM", "JOIN") {
			continue
		}
		if t.is("FROM") && i >= 2 && toks[i-1].is("DISTINCT") && toks[i-2].is("IS", "NOT") {
			continue // IS [NOT] DISTINCT FROM
		}
		if err := checkSQLFrom(toks, i+1, allow, ctes); err != nil {
			return err
		}
	}
	return nil
}

// sqlWriteWords are statements and keywords that change a database or the connection.
var sqlWriteWords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "CREATE": true, "DROP": true,
	"ALTER": true, "ATTACH": true, "DETACH": true, "PRAGMA": true, "VACUUM": true, "REINDEX": true,
	"ANALYZE": true, "BEGIN": true, "COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true,
	"RELEASE": true,
}

// sqlBannedFuncs reach files or load code.
var sqlBannedFuncs = map[string]bool{"load_extension": true, "readfile": true, "writefile": true, "edit": true, "fts3_tokenizer": true}

// sqlTableFuncs are the table-valued functions a query may read from.
var sqlTableFuncs = map[string]bool{"json_each": true, "json_tree": true}

// sqlClauseEnd ends a FROM list.
var sqlClauseEnd = map[string]bool{"WHERE": true, "GROUP": true, "ORDER": true, "LIMIT": true, "HAVING": true,
	"WINDOW": true, "UNION": true, "EXCEPT": true, "INTERSECT": true, "RETURNING": true}

// sqlJoinWords separate the items of a FROM list.
var sqlJoinWords = map[string]bool{"NATURAL": true, "LEFT": true, "RIGHT": true, "FULL": true, "INNER": true,
	"OUTER": true, "CROSS": true, "JOIN": true}

func deniedSQL(why string) error { return fmt.Errorf("%w: %s", ErrDenied, why) }

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

// checkSQLFrom checks the table list that starts at toks[i].
func checkSQLFrom(toks []sqlToken, i int, allow, ctes []string) error {
	item := true // the next token starts a table
	for i < len(toks) {
		t := toks[i]
		switch {
		case t.text == ")" || t.text == ";" || t.kind == tokIdent && !t.quoted && sqlClauseEnd[strings.ToUpper(t.text)]:
			return nil
		case t.text == ",":
			item = true
			i++
		case t.kind == tokIdent && !t.quoted && sqlJoinWords[strings.ToUpper(t.text)]:
			item = t.is("JOIN") || item
			i++
		case t.is("ON"):
			i = skipSQLExpr(toks, i+1)
		case t.is("USING"):
			i = skipSQLParens(toks, i+1)
		case t.text == "(" && item:
			if i+1 < len(toks) && toks[i+1].is("SELECT", "WITH", "VALUES") {
				i = skipSQLParens(toks, i) // a subquery: its own FROM is checked on its own
			} else {
				if err := checkSQLFrom(toks, i+1, allow, ctes); err != nil {
					return err
				}
				i = skipSQLParens(toks, i)
			}
			item = false
		case t.kind == tokIdent && item:
			name := t.text
			i++
			if i+1 < len(toks) && toks[i].text == "." && toks[i+1].kind == tokIdent {
				if !strings.EqualFold(name, "main") {
					return deniedSQL("only the main database is read")
				}
				name = toks[i+1].text
				i += 2
			}
			if i < len(toks) && toks[i].text == "(" {
				if !sqlTableFuncs[strings.ToLower(name)] {
					return deniedSQL(name + "() is not read")
				}
				i = skipSQLParens(toks, i)
			} else if !containsFold(allow, name) && !containsFold(ctes, name) {
				return deniedSQL("the table " + name + " is not one this agent reads")
			}
			item = false
		default:
			i++ // an alias, AS, INDEXED BY
		}
	}
	return nil
}

// skipSQLParens returns the index after the parenthesis group that starts at toks[i].
func skipSQLParens(toks []sqlToken, i int) int {
	if i >= len(toks) || toks[i].text != "(" {
		return i
	}
	depth := 0
	for ; i < len(toks); i++ {
		switch toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return i
}

// skipSQLExpr skips a join condition: to the next join word, comma or clause end outside
// parentheses.
func skipSQLExpr(toks []sqlToken, i int) int {
	for i < len(toks) {
		t := toks[i]
		if t.text == "(" {
			i = skipSQLParens(toks, i)
			continue
		}
		if t.text == ")" || t.text == "," || t.text == ";" || t.kind == tokIdent && !t.quoted && (sqlClauseEnd[strings.ToUpper(t.text)] || sqlJoinWords[strings.ToUpper(t.text)]) {
			return i
		}
		i++
	}
	return i
}

// sqlCTENames are the names a WITH clause defines: name [(columns)] AS [NOT] [MATERIALIZED] (.
func sqlCTENames(toks []sqlToken) []string {
	var out []string
	for i, t := range toks {
		if t.kind != tokIdent {
			continue
		}
		j := i + 1
		if j < len(toks) && toks[j].text == "(" {
			j = skipSQLParens(toks, j)
		}
		if j < len(toks) && toks[j].is("AS") {
			j++
			for j < len(toks) && toks[j].is("NOT", "MATERIALIZED") {
				j++
			}
			if j < len(toks) && toks[j].text == "(" && j+1 < len(toks) && toks[j+1].is("SELECT", "WITH", "VALUES") {
				out = append(out, t.text)
			}
		}
	}
	return out
}

type tokKind int

const (
	tokIdent tokKind = iota
	tokOther
)

type sqlToken struct {
	kind   tokKind
	text   string // an identifier without its quotes
	quoted bool
}

func (t sqlToken) is(words ...string) bool {
	if t.kind != tokIdent || t.quoted {
		return false
	}
	return slices.ContainsFunc(words, func(w string) bool { return strings.EqualFold(w, t.text) })
}

// sqlTokens splits a query into identifiers and other tokens, dropping comments, string
// literals and numbers' digits as values.
func sqlTokens(q string) ([]sqlToken, error) {
	var out []sqlToken
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			for i < len(q) && q[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				return nil, deniedSQL("an unterminated comment")
			}
			i += end + 4
		case c == '\'':
			j, err := sqlQuoted(q, i, '\'')
			if err != nil {
				return nil, err
			}
			out = append(out, sqlToken{kind: tokOther, text: "'"})
			i = j
		case c == '"' || c == '`':
			j, err := sqlQuoted(q, i, c)
			if err != nil {
				return nil, err
			}
			name := strings.ReplaceAll(q[i+1:j-1], string([]byte{c, c}), string(c))
			out = append(out, sqlToken{kind: tokIdent, text: name, quoted: true})
			i = j
		case c == '[':
			end := strings.IndexByte(q[i:], ']')
			if end < 0 {
				return nil, deniedSQL("an unterminated name")
			}
			out = append(out, sqlToken{kind: tokIdent, text: q[i+1 : i+end], quoted: true})
			i += end + 1
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80:
			j := i
			for j < len(q) && (q[j] == '_' || q[j] == '$' || q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z' || q[j] >= '0' && q[j] <= '9' || q[j] >= 0x80) {
				j++
			}
			out = append(out, sqlToken{kind: tokIdent, text: q[i:j]})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(q) && (q[j] == '.' || q[j] == '_' || q[j] >= '0' && q[j] <= '9' || q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z') {
				j++
			}
			out = append(out, sqlToken{kind: tokOther, text: "0"})
			i = j
		case c == '?' || c == ':' || c == '@' || c == '$':
			j := i + 1
			for j < len(q) && (q[j] == '_' || q[j] >= 'a' && q[j] <= 'z' || q[j] >= 'A' && q[j] <= 'Z' || q[j] >= '0' && q[j] <= '9') {
				j++
			}
			out = append(out, sqlToken{kind: tokOther, text: "?"})
			i = j
		default:
			out = append(out, sqlToken{kind: tokOther, text: string(c)})
			i++
		}
	}
	return out, nil
}

// sqlQuoted returns the index after a quoted run that starts at q[i] (a doubled quote is
// one quote inside it).
func sqlQuoted(q string, i int, quote byte) (int, error) {
	for j := i + 1; j < len(q); j++ {
		if q[j] != quote {
			continue
		}
		if j+1 < len(q) && q[j+1] == quote {
			j++
			continue
		}
		return j + 1, nil
	}
	return 0, deniedSQL("an unterminated quote")
}
