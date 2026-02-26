package spinner

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, "loading")
	if s == nil {
		t.Fatal("New returned nil")
	}
	if s.message != "loading" {
		t.Errorf("message = %q, want %q", s.message, "loading")
	}
}

func TestStartStop(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, "working")
	s.Start()
	time.Sleep(200 * time.Millisecond)
	s.Stop()

	output := buf.String()
	if output == "" {
		t.Error("expected spinner output, got empty string")
	}
	// Should contain the message
	if !strings.Contains(output, "working") {
		t.Errorf("output should contain message 'working', got %q", output)
	}
	// Should contain at least one spinner frame
	hasFrame := false
	for _, f := range frames {
		if strings.Contains(output, f) {
			hasFrame = true
			break
		}
	}
	if !hasFrame {
		t.Error("output should contain at least one spinner frame")
	}
}

func TestStopWithMessage(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, "downloading")
	s.Start()
	time.Sleep(200 * time.Millisecond)
	s.StopWithMessage("✓ Downloaded")

	output := buf.String()
	if !strings.Contains(output, "✓ Downloaded") {
		t.Errorf("output should contain final message, got %q", output)
	}
}

func TestDoubleStart(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, "test")
	s.Start()
	s.Start() // should be a no-op
	time.Sleep(100 * time.Millisecond)
	s.Stop()
}

func TestStopWithoutStart(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, "test")
	s.Stop() // should be a no-op, not panic
}

func TestStopWithMessageWithoutStart(t *testing.T) {
	var buf bytes.Buffer
	s := New(&buf, "test")
	s.StopWithMessage("done") // should be a no-op, not panic
}
