package host

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

// bindSQL puts a query's arguments into its text as SQL literals, for a reader that runs a
// program on another machine and so cannot bind them (the sqlite3 shell). Only ? marks
// outside strings, quoted names and comments are arguments; numbered and named marks are
// refused. Values are NULL, integers, finite floats, UTF-8 text without NUL, and bytes.
func bindSQL(q string, args []any) (string, error) {
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == '\'' || c == '"' || c == '`' || c == '[':
			end, err := quotedEnd(q, i)
			if err != nil {
				return "", err
			}
			b.WriteString(q[i:end])
			i = end
			continue
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			end := strings.IndexByte(q[i:], '\n')
			if end < 0 {
				end = len(q) - i
			}
			b.WriteString(q[i : i+end])
			i += end
			continue
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				return "", fmt.Errorf("%w: a comment that does not end", agent.ErrDenied)
			}
			b.WriteString(q[i : i+2+end+2])
			i += 2 + end + 2
			continue
		case c == '?':
			if i+1 < len(q) && q[i+1] >= '0' && q[i+1] <= '9' {
				return "", fmt.Errorf("%w: numbered arguments (?N) are not bound", agent.ErrDenied)
			}
			if n >= len(args) {
				return "", fmt.Errorf("the query has more ? than its %d argument(s)", len(args))
			}
			lit, err := sqlLiteral(args[n])
			if err != nil {
				return "", fmt.Errorf("argument %d: %w", n+1, err)
			}
			if strings.HasPrefix(lit, "-") {
				lit = "(" + lit + ")" // never "--", a comment, after a minus
			}
			b.WriteString(lit)
			n++
			i++
			continue
		case c == ':' || c == '@' || c == '$':
			if i+1 < len(q) && (isNameByte(q[i+1])) && (i == 0 || !isNameByte(q[i-1])) {
				return "", fmt.Errorf("%w: named arguments (%c…) are not bound", agent.ErrDenied, c)
			}
		}
		b.WriteByte(c)
		i++
	}
	if n != len(args) {
		return "", fmt.Errorf("the query has %d ? for %d argument(s)", n, len(args))
	}
	return b.String(), nil
}

func isNameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// quotedEnd is the index just after the string or quoted name that starts at i (a
// doubled quote inside is the quote itself; [names] end at the first ]).
func quotedEnd(q string, i int) (int, error) {
	closer := q[i]
	if closer == '[' {
		closer = ']'
	}
	for j := i + 1; j < len(q); j++ {
		if q[j] != closer {
			continue
		}
		if closer != ']' && j+1 < len(q) && q[j+1] == closer {
			j++
			continue
		}
		return j + 1, nil
	}
	return 0, fmt.Errorf("%w: a quote that does not end", agent.ErrDenied)
}

// sqlLiteral is a value as an SQL literal.
func sqlLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if x {
			return "1", nil
		}
		return "0", nil
	case int:
		return strconv.FormatInt(int64(x), 10), nil
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint8:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint:
		if uint64(x) > math.MaxInt64 {
			return "", fmt.Errorf("%d is larger than SQLite's integers", x)
		}
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		if x > math.MaxInt64 {
			return "", fmt.Errorf("%d is larger than SQLite's integers", x)
		}
		return strconv.FormatUint(x, 10), nil
	case float32:
		return floatLiteral(float64(x))
	case float64:
		return floatLiteral(x)
	case string:
		if !utf8.ValidString(x) || strings.ContainsRune(x, 0) {
			return "", fmt.Errorf("text with a NUL or that is not UTF-8 is not bound")
		}
		return "'" + strings.ReplaceAll(x, "'", "''") + "'", nil
	case []byte:
		return "X'" + hex.EncodeToString(x) + "'", nil
	case agent.SessionID:
		return sqlLiteral(string(x))
	}
	return "", fmt.Errorf("a %T is not bound", v)
}

func floatLiteral(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("%v is not bound", f)
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEn") {
		s += ".0" // stays a REAL
	}
	return s, nil
}
