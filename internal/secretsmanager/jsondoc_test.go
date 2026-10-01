package secretsmanager

import (
	"errors"
	"strings"
	"testing"
)

func TestSetKey(t *testing.T) {
	tests := []struct {
		name, doc, key, raw, want string
	}{
		{"replace compact", `{"a":"1","b":"2"}`, "a", `"x"`, `{"a":"x","b":"2"}`},
		{"replace keeps spacing", "{\n  \"a\": 1,\n  \"b\": 2\n}", "b", `3`, "{\n  \"a\": 1,\n  \"b\": 3\n}"},
		{"append compact", `{"a":"1"}`, "b", `"2"`, `{"a":"1","b":"2"}`},
		{"append pretty", "{\n  \"a\": \"1\"\n}", "b", `"2"`, "{\n  \"a\": \"1\",\n  \"b\": \"2\"\n}"},
		{"append to empty", `{}`, "a", `"1"`, `{"a":"1"}`},
		{"escaped key", `{"a\"b":"1"}`, `a"b`, `"2"`, `{"a\"b":"2"}`},
		{"nested untouched", `{"a":{"b":"1"},"c":"2"}`, "c", `"3"`, `{"a":{"b":"1"},"c":"3"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SetKey(tt.doc, tt.key, tt.raw)
			if err != nil {
				t.Fatalf("SetKey: %v", err)
			}
			if got != tt.want {
				t.Errorf("SetKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetKeyErrors(t *testing.T) {
	for _, doc := range []string{`["a"]`, `plain`, `{"a":"1","a":"2"}`} {
		if _, err := SetKey(doc, "a", `"x"`); err == nil {
			t.Errorf("SetKey(%q) succeeded, want error", doc)
		}
	}
}

func TestGetKey(t *testing.T) {
	doc := `{"user":"admin","port":5432,"opts":{"ssl":true}}`
	tests := []struct {
		key, want string
		found     bool
	}{
		{"user", `"admin"`, true},
		{"port", `5432`, true},
		{"opts", `{"ssl":true}`, true},
		{"missing", "", false},
	}
	for _, tt := range tests {
		raw, found, err := GetKey(doc, tt.key)
		if err != nil {
			t.Fatalf("GetKey(%q): %v", tt.key, err)
		}
		if raw != tt.want || found != tt.found {
			t.Errorf("GetKey(%q) = %q, %v; want %q, %v", tt.key, raw, found, tt.want, tt.found)
		}
	}
}

func TestKeyEditRoundTrip(t *testing.T) {
	tests := []struct {
		raw, text string
		isString  bool
	}{
		{`"p@ss<&>"`, "p@ss<&>", true},
		{`"line1\nline2"`, "line1\nline2", true},
		{`5432`, "5432", false},
		{`{"ssl":true}`, `{"ssl":true}`, false},
	}
	for _, tt := range tests {
		text, isString := KeyEditText(tt.raw)
		if text != tt.text || isString != tt.isString {
			t.Errorf("KeyEditText(%q) = %q, %v", tt.raw, text, isString)
		}
		raw, err := KeyRawFromEdit(text, isString)
		if err != nil {
			t.Fatalf("KeyRawFromEdit: %v", err)
		}
		if raw != tt.raw {
			t.Errorf("round trip of %q gave %q", tt.raw, raw)
		}
	}
}

func TestValidatePosition(t *testing.T) {
	err := Validate("{\n  \"a\": \"1\",\n}")
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("Validate error = %v, want *SyntaxError", err)
	}
	if se.Line != 3 || se.Column != 1 {
		t.Errorf("position = %d:%d, want 3:1", se.Line, se.Column)
	}
}

func TestPrettyAndCompact(t *testing.T) {
	compact := `{"b":"2","a":1.50}`
	if !IsCompact(compact) {
		t.Fatal("IsCompact(compact) = false")
	}
	pretty, err := Pretty(compact)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"b\": \"2\",\n  \"a\": 1.50\n}\n"; pretty != want {
		t.Errorf("Pretty = %q, want %q", pretty, want)
	}
	back, err := Compact(pretty)
	if err != nil || back != compact {
		t.Errorf("Compact(Pretty(x)) = %q, %v; want %q", back, err, compact)
	}
}

// Syntax is all that is checked: a number beyond float64 is valid JSON.
func TestValidateLargeNumber(t *testing.T) {
	if err := Validate(`{"n":1e400}`); err != nil {
		t.Errorf("Validate(1e400) = %v, want nil", err)
	}
}

// The offending character is part of the secret and must not be echoed.
func TestValidateRedactsCharacter(t *testing.T) {
	for _, doc := range []string{`{"a": s3cret}`, `{"a": 'q'}`, `{"a":1} Z`} {
		err := Validate(doc)
		if err == nil {
			t.Fatalf("Validate(%q) = nil", doc)
		}
		msg := err.Error()
		for _, leak := range []string{"'s'", `'\''`, "'Z'"} {
			if strings.Contains(msg, leak) {
				t.Errorf("Validate(%q) = %q, leaks %s", doc, msg, leak)
			}
		}
		if !strings.Contains(msg, "invalid character") {
			t.Errorf("Validate(%q) = %q, want the parser's reason kept", doc, msg)
		}
	}
}

func TestSetKeyCompactsNestedInCompactDoc(t *testing.T) {
	got, err := SetKey(`{"a":{"x":1}}`, "a", "{\n  \"x\": 2\n}")
	if err != nil || got != `{"a":{"x":2}}` {
		t.Errorf("SetKey = %q, %v", got, err)
	}
}

func TestKeyRawFromEditRejectsInvalidUTF8(t *testing.T) {
	if _, err := KeyRawFromEdit("a\xffb", true); err == nil {
		t.Error("KeyRawFromEdit accepted invalid UTF-8")
	}
}
