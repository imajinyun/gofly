// Package spinner provides a simple terminal spinner for long-running
// operations. It writes to stderr and respects the --quiet global flag.
package spinner

import (
	"fmt"
	"os"
	"sync"
	"time"
)

const spinChars = `|/-\`

// Spinner displays an animated indicator on stderr until Stop is called.
type Spinner struct {
	mu       sync.Mutex
	msg      string
	stop     chan struct{}
	running  bool
	disabled bool
}

// New creates a Spinner. It is initially stopped; call Start to begin.
func New() *Spinner {
	return &Spinner{stop: make(chan struct{}, 1)}
}

// Disable turns off all spinner output. Useful when --quiet is set.
func (s *Spinner) Disable() {
	s.mu.Lock()
	s.disabled = true
	s.mu.Unlock()
}

// Start begins the spinner animation with the given message.
func (s *Spinner) Start(msg string) {
	s.mu.Lock()
	if s.running || s.disabled {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.msg = msg
	s.mu.Unlock()

	go s.spin()
}

// Stop halts the spinner and clears the line. If a final message is given
// it replaces the spinner line, preserving it in the terminal history.
func (s *Spinner) Stop(final ...string) {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	s.stop <- struct{}{}
	clearLine()
	if len(final) > 0 && final[0] != "" {
		_, _ = fmt.Fprintln(os.Stderr, final[0])
	}
}

func (s *Spinner) spin() {
	i := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// Print initial frame immediately.
	s.mu.Lock()
	msg := s.msg
	s.mu.Unlock()
	printFrame(rune(spinChars[i%len(spinChars)]), msg)
	i++

	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			msg = s.msg
			s.mu.Unlock()
			printFrame(rune(spinChars[i%len(spinChars)]), msg)
			i++
		case <-s.stop:
			return
		}
	}
}

func printFrame(ch rune, msg string) {
	_, _ = fmt.Fprintf(os.Stderr, "\r\x1b[2K  %c  %s", ch, msg)
}

// Update changes the message displayed by the spinner while it runs.
func (s *Spinner) Update(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// clearLine erases the current terminal line.
func clearLine() {
	_, _ = fmt.Fprint(os.Stderr, "\r\x1b[2K")
}
