package diff

import (
	"strings"
	"testing"
)

func TestGenerate_NoChanges(t *testing.T) {
	result := Generate("hello\nworld\n", "hello\nworld\n", "original", "modified")
	if result != "" {
		t.Errorf("expected empty diff, got:\n%s", result)
	}
}

func TestGenerate_SimpleChange(t *testing.T) {
	orig := "<html>\n  <title>Old Title</title>\n</html>\n"
	mod := "<html>\n  <title>New Title</title>\n</html>\n"
	result := Generate(orig, mod, "original", "modified")

	if !strings.Contains(result, "--- original") {
		t.Error("missing original label")
	}
	if !strings.Contains(result, "+++ modified") {
		t.Error("missing modified label")
	}
	if !strings.Contains(result, "-  <title>Old Title</title>") {
		t.Error("missing deletion line")
	}
	if !strings.Contains(result, "+  <title>New Title</title>") {
		t.Error("missing addition line")
	}
}

func TestGenerate_Addition(t *testing.T) {
	orig := "line1\nline2\n"
	mod := "line1\nline2\nline3\n"
	result := Generate(orig, mod, "a", "b")
	if !strings.Contains(result, "+line3") {
		t.Errorf("expected addition, got:\n%s", result)
	}
}

func TestGenerate_Deletion(t *testing.T) {
	orig := "line1\nline2\nline3\n"
	mod := "line1\nline3\n"
	result := Generate(orig, mod, "a", "b")
	if !strings.Contains(result, "-line2") {
		t.Errorf("expected deletion, got:\n%s", result)
	}
}
