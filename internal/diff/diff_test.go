package diff

import (
	"fmt"
	"strings"
	"testing"
)

func TestGenerate_NoChanges(t *testing.T) {
	result, tooLarge := Generate("hello\nworld\n", "hello\nworld\n", "original", "modified")
	if result != "" {
		t.Errorf("expected empty diff, got:\n%s", result)
	}
	if tooLarge {
		t.Error("did not expect tooLarge for identical input")
	}
}

func TestGenerate_SimpleChange(t *testing.T) {
	orig := "<html>\n  <title>Old Title</title>\n</html>\n"
	mod := "<html>\n  <title>New Title</title>\n</html>\n"
	result, _ := Generate(orig, mod, "original", "modified")

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
	result, _ := Generate(orig, mod, "a", "b")
	if !strings.Contains(result, "+line3") {
		t.Errorf("expected addition, got:\n%s", result)
	}
}

func TestGenerate_Deletion(t *testing.T) {
	orig := "line1\nline2\nline3\n"
	mod := "line1\nline3\n"
	result, _ := Generate(orig, mod, "a", "b")
	if !strings.Contains(result, "-line2") {
		t.Errorf("expected deletion, got:\n%s", result)
	}
}

func TestGenerate_LocalChangeInLargeFile(t *testing.T) {
	// A localized edit in a large file must still produce a diff thanks to
	// common prefix/suffix trimming (the changed region is tiny).
	var orig, mod strings.Builder
	for i := 0; i < 100000; i++ {
		fmt.Fprintf(&orig, "line %d\n", i)
		if i == 50000 {
			mod.WriteString("CHANGED\n")
		} else {
			fmt.Fprintf(&mod, "line %d\n", i)
		}
	}
	result, tooLarge := Generate(orig.String(), mod.String(), "a", "b")
	if tooLarge {
		t.Fatal("localized change should not be reported as too large")
	}
	if !strings.Contains(result, "+CHANGED") {
		t.Errorf("expected changed line in diff, got:\n%s", result)
	}
}

func TestGenerate_TooLarge(t *testing.T) {
	// A large changed region (no common prefix/suffix to trim) is skipped.
	var orig, mod strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&orig, "a%d\n", i)
		fmt.Fprintf(&mod, "b%d\n", i)
	}
	result, tooLarge := Generate(orig.String(), mod.String(), "a", "b")
	if !tooLarge {
		t.Error("expected tooLarge for a large fully-changed region")
	}
	if result != "" {
		t.Errorf("expected empty diff when too large, got:\n%s", result)
	}
}
