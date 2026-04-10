package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mutapod/mutapod/internal/config"
)

const (
	filename    = "AGENTS.md"
	beginMarker = "<!-- mutapod:begin -->"
	endMarker   = "<!-- mutapod:end -->"
)

// Ensure writes or updates the mutapod-managed AGENTS.md section in the
// project directory. Any user-authored content outside the managed block is
// preserved.
func Ensure(cfg *config.Config) (string, error) {
	path := filepath.Join(cfg.Dir, filename)
	block := renderManagedBlock(cfg)

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("agents: read %s: %w", path, err)
	}

	var updated string
	if os.IsNotExist(err) {
		updated = block
	} else {
		updated = mergeManagedBlock(string(data), block)
	}

	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return "", fmt.Errorf("agents: write %s: %w", path, err)
	}
	return path, nil
}

func renderManagedBlock(cfg *config.Config) string {
	composeFile := cfg.Compose.File
	if composeFile == "" {
		composeFile = "auto-detect (compose.yaml / docker-compose.yaml family)"
	}

	lines := []string{
		beginMarker,
		"## Mutapod",
		"",
		"This repository is developed with `mutapod`.",
		"",
		"Key workflow:",
		"- Start the environment with `mutapod up`.",
		"- Use `mutapod up local` if you want a local VS Code window instead of the attached-container window.",
		"- Use `mutapod up --build` when container images need rebuilding.",
		"- Stop the environment with `mutapod down`.",
		"- Check VM/session state with `mutapod status` and `mutapod leases`.",
		"- Open a shell on the VM with `mutapod ssh`.",
		"",
		"Project setup:",
		fmt.Sprintf("- Workspace name: `%s`", cfg.Name),
		fmt.Sprintf("- Provider: `%s`", cfg.Provider.Type),
		fmt.Sprintf("- Remote workspace path: `%s`", cfg.WorkspacePath()),
		fmt.Sprintf("- Sync mode: `%s`", cfg.Sync.Mode),
		fmt.Sprintf("- Compose file: `%s`", composeFile),
		fmt.Sprintf("- VS Code workspace wrapper: `%s`", "mutapod.code-workspace"),
		"",
		"Important troubleshooting notes:",
		"- Source of truth is `mutapod.yaml` in this repository.",
		"- Code edits are local and synchronized to the remote VM with Mutagen.",
		"- Docker Compose runs on the remote VM, not on the local machine.",
		"- `mutapod up` waits for the initial sync flush and checks for Mutagen transition problems before building or opening VS Code.",
		"- If a remote build/runtime issue looks stale, rerun with `mutapod up --build`.",
	}

	if cfg.Compose.PrimaryService != "" {
		lines = append(lines, fmt.Sprintf("- Primary service for attached-container workflows: `%s`", cfg.Compose.PrimaryService))
	}
	if cfg.Compose.WorkspaceFolder != "" {
		lines = append(lines, fmt.Sprintf("- In-container workspace folder: `%s`", cfg.Compose.WorkspaceFolder))
	}

	lines = append(lines,
		"",
		"This section is managed by mutapod. You can add your own instructions elsewhere in this file.",
		endMarker,
		"",
	)

	return strings.Join(lines, "\n")
}

func mergeManagedBlock(existing, block string) string {
	start := strings.Index(existing, beginMarker)
	end := strings.Index(existing, endMarker)
	if start >= 0 && end >= 0 && end >= start {
		end += len(endMarker)
		merged := existing[:start] + block + existing[end:]
		return normalizeSpacing(merged)
	}

	existing = strings.TrimRight(existing, "\r\n")
	if existing == "" {
		return block
	}
	return normalizeSpacing(existing + "\n\n" + block)
}

func normalizeSpacing(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}
