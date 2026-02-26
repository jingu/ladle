// Package spinner provides a terminal spinner for indicating ongoing operations.
package spinner

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// frames is the set of Braille characters used for the spinner animation.
var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner displays an animated spinner with a message on the given writer.
type Spinner struct {
	w       io.Writer
	message string
	done    chan struct{}
	stopped chan struct{}
	mu      sync.Mutex
	running bool
}

// New creates a new Spinner that writes to w with the given message.
func New(w io.Writer, message string) *Spinner {
	return &Spinner{
		w:       w,
		message: message,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

// Start begins the spinner animation in a background goroutine.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		defer close(s.stopped)

		i := 0
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				fmt.Fprintf(s.w, "\r\033[K%s %s", frames[i%len(frames)], s.message)
				i++
			}
		}
	}()
}

// Stop stops the spinner and clears the line.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.done)
	s.mu.Unlock()

	// Wait for the goroutine to finish before writing to avoid data race.
	<-s.stopped
	fmt.Fprintf(s.w, "\r\033[K")
}

// StopWithMessage stops the spinner and replaces it with a final message.
func (s *Spinner) StopWithMessage(msg string) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.done)
	s.mu.Unlock()

	// Wait for the goroutine to finish before writing to avoid data race.
	<-s.stopped
	fmt.Fprintf(s.w, "\r\033[K%s\n", msg)
}
