// Package ignore parses .mutapodignore files (gitignore syntax) and produces
// the list of --ignore flags for mutagen sync/forward commands.
package ignore

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Filename = ".mutapodignore"

var defaultPatterns = []string{
	"mutapod.code-workspace",
}

// Patterns holds the parsed ignore patterns.
type Patterns struct {
	lines []string
}

// Load reads .mutapodignore from dir. If the file does not exist, an empty
// Patterns is returned (no error).
func Load(dir string) (*Patterns, error) {
	path := filepath.Join(dir, Filename)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Patterns{lines: append([]string(nil), defaultPatterns...)}, nil
		}
		return nil, fmt.Errorf("ignore: open %s: %w", path, err)
	}
	defer f.Close()

	lines := append([]string(nil), defaultPatterns...)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		// Skip blank lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if contains(lines, trimmed) {
			continue
		}
		lines = append(lines, trimmed)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ignore: read %s: %w", path, err)
	}
	return &Patterns{lines: lines}, nil
}

// MutagenFlags returns the list of --ignore <pattern> flag pairs for mutagen.
// Each pattern produces two elements: "--ignore" and the pattern string.
func (p *Patterns) MutagenFlags() []string {
	if len(p.lines) == 0 {
		return nil
	}
	flags := make([]string, 0, len(p.lines)*2)
	for _, line := range p.lines {
		flags = append(flags, "--ignore", line)
	}
	return flags
}

// Patterns returns the raw pattern strings.
func (p *Patterns) Patterns() []string {
	out := make([]string, len(p.lines))
	copy(out, p.lines)
	return out
}

// Len returns the number of patterns.
func (p *Patterns) Len() int {
	return len(p.lines)
}

// Signature returns a stable digest of the effective ignore pattern set.
func (p *Patterns) Signature() string {
	sum := sha256.Sum256([]byte(strings.Join(p.lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
