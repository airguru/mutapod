package ignore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeIgnore(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0644); err != nil {
		t.Fatalf("write .mutapodignore: %v", err)
	}
}

func TestLoad_NotExist(t *testing.T) {
	dir := t.TempDir()
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"mutapod.code-workspace"}
	if !reflect.DeepEqual(p.Patterns(), want) {
		t.Errorf("patterns: got %v, want %v", p.Patterns(), want)
	}
}

func TestLoad_Patterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnore(t, dir, `
# this is a comment
.git
node_modules

# blank lines ignored
__pycache__
*.pyc
.venv
`)
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"mutapod.code-workspace", ".git", "node_modules", "__pycache__", "*.pyc", ".venv"}
	if !reflect.DeepEqual(p.Patterns(), want) {
		t.Errorf("patterns: got %v, want %v", p.Patterns(), want)
	}
}

func TestLoad_EnvFilesAreIncluded(t *testing.T) {
	// .env files should NOT be excluded by default — user controls this entirely
	dir := t.TempDir()
	writeIgnore(t, dir, `.git
node_modules
`)
	p, _ := Load(dir)
	for _, pat := range p.Patterns() {
		if pat == ".env" || pat == ".env.*" {
			t.Errorf("found unexpected default exclude %q — .env files should not be excluded by default", pat)
		}
	}
}

func TestMutagenFlags(t *testing.T) {
	dir := t.TempDir()
	writeIgnore(t, dir, `.git
node_modules
*.log
`)
	p, _ := Load(dir)
	flags := p.MutagenFlags()
	want := []string{"--ignore", "mutapod.code-workspace", "--ignore", ".git", "--ignore", "node_modules", "--ignore", "*.log"}
	if !reflect.DeepEqual(flags, want) {
		t.Errorf("flags: got %v, want %v", flags, want)
	}
}

func TestLoad_DoesNotDuplicateDefaultPattern(t *testing.T) {
	dir := t.TempDir()
	writeIgnore(t, dir, `mutapod.code-workspace
.git
`)
	p, _ := Load(dir)
	want := []string{"mutapod.code-workspace", ".git"}
	if !reflect.DeepEqual(p.Patterns(), want) {
		t.Errorf("patterns: got %v, want %v", p.Patterns(), want)
	}
}

func TestMutagenFlags_DefaultOnly(t *testing.T) {
	p := &Patterns{lines: []string{"mutapod.code-workspace"}}
	want := []string{"--ignore", "mutapod.code-workspace"}
	if flags := p.MutagenFlags(); !reflect.DeepEqual(flags, want) {
		t.Errorf("flags: got %v, want %v", flags, want)
	}
}
