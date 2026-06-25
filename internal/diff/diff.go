// Package diff provides unified diff generation and display.
package diff

import (
	"fmt"
	"io"
	"strings"
)

const (
	// maxDiffLines caps the per-side line count fed to the O(n*m) LCS algorithm.
	// It is applied after trimming common leading/trailing lines, so a localized
	// edit in a huge file still produces a diff; only a genuinely large changed
	// region is skipped to avoid pathological memory/time use.
	maxDiffLines = 5000
	// maxDiffBytes caps the byte size of the changed region. It guards against a
	// few enormous lines that are cheap for LCS but huge to render.
	maxDiffBytes = 2 << 20 // 2 MiB
)

// Generate produces a unified diff between two texts. The second return value is
// true when the changed region is too large to diff; in that case the diff text
// is empty and the caller should skip showing a diff (the texts still differ).
func Generate(original, modified, labelOrig, labelMod string) (string, bool) {
	origLines := splitLines(original)
	modLines := splitLines(modified)

	// Trim common leading/trailing lines so only the changed region goes through
	// the quadratic LCS. This keeps diffs cheap for localized edits in large files.
	pre := commonPrefix(origLines, modLines)
	suf := commonSuffix(origLines[pre:], modLines[pre:])
	origMid := origLines[pre : len(origLines)-suf]
	modMid := modLines[pre : len(modLines)-suf]

	if len(origMid) == 0 && len(modMid) == 0 {
		return "", false
	}

	// Skip the diff if the changed region is too large in either dimension.
	if len(origMid) > maxDiffLines || len(modMid) > maxDiffLines ||
		byteLen(origMid) > maxDiffBytes || byteLen(modMid) > maxDiffBytes {
		return "", true
	}

	edits := lcsEdits(origMid, modMid)

	// Re-attach the trimmed common lines as context so hunk line numbers are
	// correct. This is linear work; only lcsEdits above is quadratic.
	full := make([]edit, 0, pre+len(edits)+suf)
	for _, l := range origLines[:pre] {
		full = append(full, edit{' ', l})
	}
	full = append(full, edits...)
	for _, l := range origLines[len(origLines)-suf:] {
		full = append(full, edit{' ', l})
	}

	hunks := buildHunks(full, 3)
	if len(hunks) == 0 {
		return "", false
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", labelOrig)
	fmt.Fprintf(&sb, "+++ %s\n", labelMod)

	for _, h := range hunks {
		sb.WriteString(h.String())
	}
	return sb.String(), false
}

// Print writes a colored diff to the given writer.
func Print(w io.Writer, diffText string) {
	for _, line := range strings.Split(diffText, "\n") {
		switch {
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "+++"):
			_, _ = fmt.Fprintf(w, "\033[1m%s\033[0m\n", line)
		case strings.HasPrefix(line, "@@"):
			_, _ = fmt.Fprintf(w, "\033[36m%s\033[0m\n", line)
		case strings.HasPrefix(line, "+"):
			_, _ = fmt.Fprintf(w, "\033[32m%s\033[0m\n", line)
		case strings.HasPrefix(line, "-"):
			_, _ = fmt.Fprintf(w, "\033[31m%s\033[0m\n", line)
		default:
			_, _ = fmt.Fprintln(w, line)
		}
	}
}

// commonPrefix returns the number of identical leading lines in a and b.
func commonPrefix(a, b []string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// commonSuffix returns the number of identical trailing lines in a and b.
func commonSuffix(a, b []string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}

// byteLen returns the byte size of lines, counting only the newlines between
// them (the original trailing newline, if any, is dropped by splitLines and not
// reconstructed here). Used as a coarse size guard, so an exact count is not
// required; this avoids over-counting when the input had no trailing newline.
func byteLen(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	total := len(lines) - 1 // newlines between lines
	for _, l := range lines {
		total += len(l)
	}
	return total
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type edit struct {
	op   byte // ' ', '+', '-'
	text string
}

type hunk struct {
	origStart int
	origCount int
	modStart  int
	modCount  int
	lines     []diffLine
}

type diffLine struct {
	op   byte
	text string
}

func (h *hunk) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.origStart, h.origCount, h.modStart, h.modCount)
	for _, l := range h.lines {
		sb.WriteByte(l.op)
		sb.WriteString(l.text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// lcsEdits computes the edit script between a and b using LCS dynamic programming.
func lcsEdits(a, b []string) []edit {
	n := len(a)
	m := len(b)

	// Build LCS table
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Backtrack to build edit script
	var edits []edit
	i, j := n, m
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && a[i-1] == b[j-1] {
			edits = append(edits, edit{' ', a[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			edits = append(edits, edit{'+', b[j-1]})
			j--
		} else {
			edits = append(edits, edit{'-', a[i-1]})
			i--
		}
	}

	// Reverse to get forward order
	for l, r := 0, len(edits)-1; l < r; l, r = l+1, r-1 {
		edits[l], edits[r] = edits[r], edits[l]
	}
	return edits
}

func buildHunks(edits []edit, contextLines int) []hunk {
	// Find all change ranges (contiguous runs of non-context edits)
	type changeRange struct {
		start, end int
	}
	var changes []changeRange
	i := 0
	for i < len(edits) {
		if edits[i].op != ' ' {
			start := i
			for i < len(edits) && edits[i].op != ' ' {
				i++
			}
			changes = append(changes, changeRange{start, i - 1})
		} else {
			i++
		}
	}
	if len(changes) == 0 {
		return nil
	}

	// Group changes into hunks, merging those whose context overlaps
	type hunkRange struct {
		start, end int
	}
	var hunkRanges []hunkRange

	for _, cr := range changes {
		ctxStart := cr.start - contextLines
		if ctxStart < 0 {
			ctxStart = 0
		}
		ctxEnd := cr.end + contextLines
		if ctxEnd >= len(edits) {
			ctxEnd = len(edits) - 1
		}

		if len(hunkRanges) > 0 && ctxStart <= hunkRanges[len(hunkRanges)-1].end+1 {
			hunkRanges[len(hunkRanges)-1].end = ctxEnd
		} else {
			hunkRanges = append(hunkRanges, hunkRange{ctxStart, ctxEnd})
		}
	}

	// Build actual hunks
	var hunks []hunk
	for _, hr := range hunkRanges {
		oLine := 1
		mLine := 1
		for j := 0; j < hr.start; j++ {
			switch edits[j].op {
			case ' ':
				oLine++
				mLine++
			case '-':
				oLine++
			case '+':
				mLine++
			}
		}

		h := hunk{origStart: oLine, modStart: mLine}
		oc, mc := 0, 0
		for j := hr.start; j <= hr.end; j++ {
			h.lines = append(h.lines, diffLine{edits[j].op, edits[j].text})
			switch edits[j].op {
			case ' ':
				oc++
				mc++
			case '-':
				oc++
			case '+':
				mc++
			}
		}
		h.origCount = oc
		h.modCount = mc
		hunks = append(hunks, h)
	}
	return hunks
}
