package agent

import (
	"fmt"
	"slices"
	"strings"
)

// CheckSQLQuery checks a query's text before it reaches a database: one SELECT (or WITH,
// VALUES) that writes nothing, reads only tables in allow (and the names its leading WITH
// clause defines), and never mentions a table in deny. Names compare without regard to
// case. A quoted string where a table belongs, a WITH inside the query and a schema other
// than main are refused. It is a guard on the text; a Host also refuses reads below it
// where it can (SQLTableLimiter).
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
		if t.punct(";") {
			if i != len(toks)-1 {
				return deniedSQL("one query at a time")
			}
			continue
		}
		if t.kind == tokIdent && !t.quoted && sqlWriteWords[strings.ToUpper(t.text)] {
			if strings.EqualFold(t.text, "REPLACE") && i+1 < len(toks) && toks[i+1].punct("(") {
				continue // the replace() function
			}
			return deniedSQL(strings.ToUpper(t.text) + " is not run")
		}
		if t.is("WITH") && i > 0 {
			return deniedSQL("WITH is read only at the start of a query")
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
		switch {
		case t.is("FROM", "JOIN"):
			if t.is("FROM") && i >= 2 && toks[i-1].is("DISTINCT") && toks[i-2].is("IS", "NOT") {
				continue // IS [NOT] DISTINCT FROM
			}
			if err := checkSQLFrom(toks, i+1, allow, ctes); err != nil {
				return err
			}
		case t.is("IN") && i+1 < len(toks) && !toks[i+1].punct("("):
			// expr IN table reads a table with no FROM.
			if _, err := checkSQLTable(toks, i+1, allow, ctes); err != nil {
				return err
			}
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

// SQLTableFuncs are the table-valued functions a query may read from. A Host's
// authorizer allows reads of them (SQLite reports a read of json_each.value) on top of
// the module's tables.
func SQLTableFuncs() []string { return []string{"json_each", "json_tree"} }

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
		case t.punct(")") || t.punct(";") || t.kind == tokIdent && !t.quoted && sqlClauseEnd[strings.ToUpper(t.text)]:
			return nil
		case t.punct(","):
			item = true
			i++
		case t.kind == tokIdent && !t.quoted && sqlJoinWords[strings.ToUpper(t.text)]:
			item = t.is("JOIN") || item
			i++
		case t.is("ON"):
			i = skipSQLExpr(toks, i+1)
		case t.is("USING"):
			i = skipSQLParens(toks, i+1)
		case t.punct("(") && item:
			if i+1 < len(toks) && toks[i+1].is("SELECT", "WITH", "VALUES") {
				i = skipSQLParens(toks, i) // a subquery: its own FROM is checked on its own
			} else {
				if err := checkSQLFrom(toks, i+1, allow, ctes); err != nil {
					return err
				}
				i = skipSQLParens(toks, i)
			}
			item = false
		case item:
			next, err := checkSQLTable(toks, i, allow, ctes)
			if err != nil {
				return err
			}
			i, item = next, false
		default:
			i++ // an alias, AS, INDEXED BY
		}
	}
	return nil
}

// checkSQLTable checks the table named at toks[i] ([schema.]name, or a table-valued
// function and its arguments) and returns the index after it. A quoted string there is
// refused: SQLite reads 'name' as a table.
func checkSQLTable(toks []sqlToken, i int, allow, ctes []string) (int, error) {
	if i >= len(toks) {
		return i, nil
	}
	if toks[i].kind == tokString {
		return i, deniedSQL("a quoted string is not read as a table")
	}
	if toks[i].kind != tokIdent {
		return i + 1, nil // not a name: SQLite refuses the query itself
	}
	name := toks[i].text
	i++
	if i < len(toks) && toks[i].punct(".") {
		if !strings.EqualFold(name, "main") {
			return i, deniedSQL("only the main database is read")
		}
		if i+1 >= len(toks) || toks[i+1].kind != tokIdent {
			return i, deniedSQL("a quoted string is not read as a table")
		}
		name = toks[i+1].text
		i += 2
	}
	if i < len(toks) && toks[i].punct("(") {
		if !containsFold(SQLTableFuncs(), name) {
			return i, deniedSQL(name + "() is not read")
		}
		return skipSQLParens(toks, i), nil
	}
	if !containsFold(allow, name) && !containsFold(ctes, name) {
		return i, deniedSQL("the table " + name + " is not one this agent reads")
	}
	return i, nil
}

// skipSQLParens returns the index after the parenthesis group that starts at toks[i].
func skipSQLParens(toks []sqlToken, i int) int {
	if i >= len(toks) || !toks[i].punct("(") {
		return i
	}
	depth := 0
	for ; i < len(toks); i++ {
		switch {
		case toks[i].punct("("):
			depth++
		case toks[i].punct(")"):
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
		if t.punct("(") {
			i = skipSQLParens(toks, i)
			continue
		}
		if t.punct(")") || t.punct(",") || t.punct(";") || t.kind == tokIdent && !t.quoted && (sqlClauseEnd[strings.ToUpper(t.text)] || sqlJoinWords[strings.ToUpper(t.text)]) {
			return i
		}
		i++
	}
	return i
}

// sqlCTENames are the names the query's leading WITH clause defines:
// WITH [RECURSIVE] name [(columns)] AS [NOT] [MATERIALIZED] (query), … They are visible
// across the whole query. A WITH anywhere else is refused before this is asked.
func sqlCTENames(toks []sqlToken) []string {
	if len(toks) == 0 || !toks[0].is("WITH") {
		return nil
	}
	var out []string
	i := 1
	if i < len(toks) && toks[i].is("RECURSIVE") {
		i++
	}
	for i < len(toks) && toks[i].kind == tokIdent {
		name := toks[i].text
		i++
		if i < len(toks) && toks[i].punct("(") {
			i = skipSQLParens(toks, i)
		}
		if i >= len(toks) || !toks[i].is("AS") {
			break
		}
		i++
		for i < len(toks) && toks[i].is("NOT", "MATERIALIZED") {
			i++
		}
		if i >= len(toks) || !toks[i].punct("(") {
			break
		}
		out = append(out, name)
		i = skipSQLParens(toks, i)
		if i >= len(toks) || !toks[i].punct(",") {
			break
		}
		i++
	}
	return out
}

type tokKind int

const (
	tokIdent  tokKind = iota
	tokString         // a '…' literal; text is its value
	tokOther
)

type sqlToken struct {
	kind   tokKind
	text   string // an identifier without its quotes, a string's value
	quoted bool
}

// punct reports whether t is the punctuation p (not a string or name that reads so).
func (t sqlToken) punct(p string) bool { return t.kind == tokOther && t.text == p }

func (t sqlToken) is(words ...string) bool {
	if t.kind != tokIdent || t.quoted {
		return false
	}
	return slices.ContainsFunc(words, func(w string) bool { return strings.EqualFold(w, t.text) })
}

// sqlTokens splits a query into identifiers, strings and other tokens, dropping comments
// and numbers' digits.
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
			out = append(out, sqlToken{kind: tokString, text: strings.ReplaceAll(q[i+1:j-1], "''", "'")})
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
