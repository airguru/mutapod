// Package deps ensures required local binaries (mutagen) are available,
// downloading them to ~/.mutapod/bin/ if not found on PATH.
package deps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const mutagenVersion = "0.18.1"

// MutagenPath returns the path to the mutagen binary.
// If mutagen is on PATH it is used directly; otherwise it is downloaded
// to ~/.mutapod/bin/ and that path is returned.
func MutagenPath() (string, error) {
	if path, err := exec.LookPath("mutagen"); err == nil {
		return path, nil
	}
	binDir, err := localBinDir()
	if err != nil {
		return "", err
	}
	local := filepath.Join(binDir, binaryName("mutagen"))
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	fmt.Printf("→ mutagen not found — downloading v%s...\n", mutagenVersion)
	if err := downloadMutagen(binDir); err != nil {
		return "", fmt.Errorf("deps: download mutagen: %w", err)
	}
	fmt.Printf("✓ mutagen downloaded to %s\n", binDir)
	return local, nil
}

// EnsureAll checks all required local dependencies.
func EnsureAll() error {
	if _, err := MutagenPath(); err != nil {
		return err
	}
	return nil
}

func localBinDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("deps: home dir: %w", err)
	}
	dir := filepath.Join(home, ".mutapod", "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("deps: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func downloadMutagen(binDir string) error {
	url := mutagenDownloadURL()
	resp, err := http.Get(url) //nolint:gosec // URL is a constant
	if err != nil {
		return fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if strings.HasSuffix(url, ".zip") {
		return extractZip(data, binDir)
	}
	return extractTarGz(data, binDir)
}

func mutagenDownloadURL() string {
	os_ := runtime.GOOS
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "amd64"
	} else if arch == "arm64" {
		arch = "arm64"
	}

	base := fmt.Sprintf("https://github.com/mutagen-io/mutagen/releases/download/v%s/mutagen_%s_%s_v%s",
		mutagenVersion, os_, arch, mutagenVersion)
	if os_ == "windows" {
		return base + ".zip"
	}
	return base + ".tar.gz"
}

// extractZip extracts mutagen.exe and mutagen-agents.tar.gz from a zip archive.
func extractZip(data []byte, destDir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if name != binaryName("mutagen") && name != "mutagen-agents.tar.gz" {
			continue
		}
		if err := extractZipFile(f, filepath.Join(destDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc) //nolint:gosec
	return err
}

// extractTarGz extracts mutagen and mutagen-agents.tar.gz from a .tar.gz archive.
func extractTarGz(data []byte, destDir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		name := filepath.Base(hdr.Name)
		if name != "mutagen" && name != "mutagen-agents.tar.gz" {
			continue
		}
		destPath := filepath.Join(destDir, name)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec
			out.Close()
			return err
		}
		out.Close()
	}
	return nil
}
