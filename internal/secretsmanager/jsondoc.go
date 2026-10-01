package secretsmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Secrets Manager stores key/value secrets as a JSON object, usually compact
// ({"user":"x","pass":"y"}) because that is what the console writes. The
// helpers below let ladle edit such a document readably while writing it back
// in its original shape, so an edit never produces a version whose only change
// is formatting.

// LooksLikeJSON reports whether s is meant to be JSON: its first non-space
// character opens an object or array. Used for new secrets, where there is no
// original to compare against.
func LooksLikeJSON(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

// IsJSON reports whether s is a JSON object or array.
func IsJSON(s string) bool {
	return LooksLikeJSON(s) && json.Valid([]byte(s))
}

// SyntaxError is a JSON parse failure located by line and column. It carries
// the parser's message but never an excerpt of the input, which is a secret.
type SyntaxError struct {
	Line, Column int
	Msg          string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("invalid JSON at line %d, column %d: %s", e.Line, e.Column, e.Msg)
}

// Validate returns a *SyntaxError when s is not valid JSON. It checks syntax
// only (json.Valid), so a number too large for float64 is still accepted.
func Validate(s string) error {
	if json.Valid([]byte(s)) {
		return nil
	}
	// Unmarshal into a RawMessage runs the same syntax check but reports where
	// it failed, without converting any value.
	var raw json.RawMessage
	var se *json.SyntaxError
	if err := json.Unmarshal([]byte(s), &raw); !errors.As(err, &se) {
		return &SyntaxError{Line: 1, Column: 1, Msg: "invalid JSON"}
	}
	line, col := position(s, int(se.Offset))
	return &SyntaxError{Line: line, Column: col, Msg: redactChar.ReplaceAllString(se.Error(), "invalid character")}
}

// redactChar matches the quoted input character that encoding/json puts in
// its messages (e.g. "invalid character 'x' looking for ..."). That character
// is part of the secret, so it is dropped.
var redactChar = regexp.MustCompile(`invalid character '(?:\\.|[^'\\])*'`)

// position converts the byte offset reported by encoding/json (just past the
// offending byte) into a 1-based line and column. The column counts runes,
// so it points at the character an editor shows even after multibyte text.
func position(s string, offset int) (line, col int) {
	if offset > len(s) {
		offset = len(s)
	}
	if offset > 0 {
		offset-- // point at the offending byte itself
	}
	before := s[:offset]
	line = strings.Count(before, "\n") + 1
	col = utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:]) + 1
	return line, col
}

// IsCompact reports whether the JSON document s is already in compact form.
func IsCompact(s string) bool {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		return false
	}
	return buf.String() == s
}

// Compact returns the compact form of the JSON document s.
func Compact(s string) (string, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Pretty returns s indented for editing. json.Indent only re-flows whitespace,
// so key order and number spelling are untouched.
func Pretty(s string) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return "", err
	}
	buf.WriteByte('\n')
	return buf.String(), nil
}

// member is one top-level key of a JSON object, located by byte offsets so it
// can be replaced in place.
type member struct {
	key      string
	sepStart int // just after the preceding "{" or ","
	keyStart int
	keyEnd   int
	valStart int
	valEnd   int
}

// members scans the top-level members of the JSON object doc.
func members(doc string) ([]member, error) {
	if !json.Valid([]byte(doc)) {
		return nil, Validate(doc)
	}
	i := skipSpace(doc, 0)
	if i >= len(doc) || doc[i] != '{' {
		return nil, fmt.Errorf("secret value is not a JSON object")
	}
	i++
	var out []member
	for {
		sep := i
		i = skipSpace(doc, i)
		if doc[i] == '}' {
			return out, nil
		}
		m := member{sepStart: sep, keyStart: i}
		m.keyEnd = stringEnd(doc, i)
		if err := json.Unmarshal([]byte(doc[m.keyStart:m.keyEnd]), &m.key); err != nil {
			return nil, err
		}
		i = skipSpace(doc, m.keyEnd) + 1 // past ':'
		m.valStart = skipSpace(doc, i)
		var raw json.RawMessage
		if err := json.NewDecoder(strings.NewReader(doc[m.valStart:])).Decode(&raw); err != nil {
			return nil, err
		}
		m.valEnd = m.valStart + len(raw)
		out = append(out, m)
		i = skipSpace(doc, m.valEnd)
		if doc[i] == ',' {
			i++
		}
	}
}

func skipSpace(s string, i int) int {
	for i < len(s) && strings.IndexByte(" \t\r\n", s[i]) >= 0 {
		i++
	}
	return i
}

// stringEnd returns the offset just past the JSON string starting at s[i].
func stringEnd(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(s)
}

// Keys returns the top-level keys of the JSON object doc, in document order.
func Keys(doc string) ([]string, error) {
	ms, err := members(doc)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(ms))
	for _, m := range ms {
		keys = append(keys, m.key)
	}
	return keys, nil
}

// find returns the member named key, or ok=false. A key that appears more than
// once is an error: which occurrence wins differs between JSON readers, so
// editing either would be a guess.
func find(ms []member, key string) (m member, ok bool, err error) {
	for _, c := range ms {
		if c.key != key {
			continue
		}
		if ok {
			return member{}, false, fmt.Errorf("key %q appears more than once in the secret", key)
		}
		m, ok = c, true
	}
	return m, ok, nil
}

// GetKey returns the raw JSON value of key in the object doc.
func GetKey(doc, key string) (raw string, found bool, err error) {
	ms, err := members(doc)
	if err != nil {
		return "", false, err
	}
	m, ok, err := find(ms, key)
	if err != nil || !ok {
		return "", false, err
	}
	return doc[m.valStart:m.valEnd], true, nil
}

// SetKey returns doc with key's value replaced by raw (a JSON value), or with
// key appended when absent. Everything outside the touched value is kept byte
// for byte, and an appended member copies the separator and spacing of the
// last existing one so it matches the document's style. In a compact document
// raw is compacted too.
func SetKey(doc, key, raw string) (string, error) {
	ms, err := members(doc)
	if err != nil {
		return "", err
	}
	m, ok, err := find(ms, key)
	if err != nil {
		return "", err
	}
	// An object or array value edited across several lines would otherwise
	// turn a compact document into a partly indented one.
	if IsCompact(doc) {
		if raw, err = Compact(raw); err != nil {
			return "", err
		}
	}
	if ok {
		return doc[:m.valStart] + raw + doc[m.valEnd:], nil
	}
	qkey, err := marshalString(key)
	if err != nil {
		return "", err
	}
	if len(ms) == 0 {
		return "{" + qkey + ":" + raw + "}", nil
	}
	last := ms[len(ms)-1]
	indent := doc[last.sepStart:last.keyStart]
	colon := doc[last.keyEnd:last.valStart]
	return doc[:last.valEnd] + "," + indent + qkey + colon + raw + doc[last.valEnd:], nil
}

// KeyEditText renders a raw JSON value for the editor. A string is shown
// decoded (no quotes or escapes), since that is what a password or URL looks
// like to the person editing it; any other value is shown as JSON.
func KeyEditText(raw string) (text string, isString bool) {
	var s string
	if strings.HasPrefix(raw, `"`) && json.Unmarshal([]byte(raw), &s) == nil {
		return s, true
	}
	return raw, false
}

// KeyRawFromEdit converts edited text back into a raw JSON value, the inverse
// of KeyEditText. A non-string value must be valid JSON.
func KeyRawFromEdit(text string, isString bool) (string, error) {
	if isString {
		// A JSON encoder would silently replace invalid UTF-8 with U+FFFD,
		// storing bytes other than the ones given.
		if !utf8.ValidString(text) {
			return "", fmt.Errorf("value is not valid UTF-8")
		}
		return marshalString(text)
	}
	t := strings.TrimSpace(text)
	if err := Validate(t); err != nil {
		return "", err
	}
	return t, nil
}

// marshalString encodes s as a JSON string without HTML escaping, so "<" and
// "&" in a secret stay readable in the stored document.
func marshalString(s string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
