package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckSelectsMatchingReleaseAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/airguru/mutapod/releases/latest" {
			http.NotFound(w, r)
			return
		}
		payload := githubRelease{
			HTMLURL: "https://github.com/airguru/mutapod/releases/tag/v1.2.3",
			TagName: "v1.2.3",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}{
				{Name: "mutapod_1.2.3_windows_amd64.zip", BrowserDownloadURL: serverURL(r, "/download/windows.zip")},
				{Name: "checksums.txt", BrowserDownloadURL: serverURL(r, "/download/checksums.txt")},
			},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	updater := &Updater{
		HTTPClient: server.Client(),
		APIBaseURL: server.URL,
		GOOS:       "windows",
		GOARCH:     "amd64",
	}

	status, err := updater.Check(context.Background(), "1.2.2")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status.UpToDate {
		t.Fatal("expected update to be available")
	}
	if status.Latest.AssetName != "mutapod_1.2.3_windows_amd64.zip" {
		t.Fatalf("unexpected asset name: %s", status.Latest.AssetName)
	}
	if status.Latest.ChecksumURL == "" {
		t.Fatal("expected checksum URL to be resolved")
	}
}

func TestUpdateReplacesExecutableOnUnix(t *testing.T) {
	archive := makeTarGzArchive(t, "mutapod", []byte("new binary"))
	checksum := sha256.Sum256(archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/airguru/mutapod/releases/latest":
			payload := githubRelease{
				TagName: "v1.2.3",
				Assets: []struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{
					{Name: "mutapod_1.2.3_linux_amd64.tar.gz", BrowserDownloadURL: serverURL(r, "/download/mutapod.tar.gz")},
					{Name: "checksums.txt", BrowserDownloadURL: serverURL(r, "/download/checksums.txt")},
				},
			}
			_ = json.NewEncoder(w).Encode(payload)
		case "/download/mutapod.tar.gz":
			_, _ = w.Write(archive)
		case "/download/checksums.txt":
			_, _ = w.Write([]byte(hex.EncodeToString(checksum[:]) + "  mutapod_1.2.3_linux_amd64.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), "mutapod")
	if err := os.WriteFile(target, []byte("old binary"), 0755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	updater := &Updater{
		HTTPClient:     server.Client(),
		APIBaseURL:     server.URL,
		GOOS:           "linux",
		GOARCH:         "amd64",
		ExecutablePath: target,
	}

	result, err := updater.Update(context.Background(), "1.2.2")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !result.Updated {
		t.Fatal("expected update to replace executable")
	}
	if result.PendingRestart {
		t.Fatal("expected direct replacement on unix")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "new binary" {
		t.Fatalf("unexpected target contents: %q", string(data))
	}
}

func TestUpdateStagesWindowsReplacement(t *testing.T) {
	archive := makeZipArchive(t, "mutapod.exe", []byte("new windows binary"))
	checksum := sha256.Sum256(archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/airguru/mutapod/releases/latest":
			payload := githubRelease{
				TagName: "v1.2.3",
				Assets: []struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{
					{Name: "mutapod_1.2.3_windows_amd64.zip", BrowserDownloadURL: serverURL(r, "/download/mutapod.zip")},
					{Name: "checksums.txt", BrowserDownloadURL: serverURL(r, "/download/checksums.txt")},
				},
			}
			_ = json.NewEncoder(w).Encode(payload)
		case "/download/mutapod.zip":
			_, _ = w.Write(archive)
		case "/download/checksums.txt":
			_, _ = w.Write([]byte(hex.EncodeToString(checksum[:]) + "  mutapod_1.2.3_windows_amd64.zip\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "mutapod.exe")
	if err := os.WriteFile(target, []byte("old windows binary"), 0755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var launchedScript string
	oldStart := startDetachedWindowsReplace
	startDetachedWindowsReplace = func(scriptPath string) error {
		launchedScript = scriptPath
		return nil
	}
	t.Cleanup(func() { startDetachedWindowsReplace = oldStart })

	updater := &Updater{
		HTTPClient:     server.Client(),
		APIBaseURL:     server.URL,
		GOOS:           "windows",
		GOARCH:         "amd64",
		ExecutablePath: target,
	}

	result, err := updater.Update(context.Background(), "1.2.2")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !result.PendingRestart {
		t.Fatal("expected Windows update to be staged for restart")
	}
	if launchedScript == "" {
		t.Fatal("expected Windows helper script to be launched")
	}

	scriptData, err := os.ReadFile(launchedScript)
	if err != nil {
		t.Fatalf("read helper script: %v", err)
	}
	script := string(scriptData)
	if !strings.Contains(script, "move /Y") {
		t.Fatalf("expected move command in helper script: %q", script)
	}
	if !strings.Contains(script, target) {
		t.Fatalf("expected target path in helper script: %q", script)
	}

	matches, err := filepath.Glob(filepath.Join(targetDir, ".mutapod.exe.*.new"))
	if err != nil {
		t.Fatalf("glob staged binary: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one staged binary, got %d", len(matches))
	}
}

func TestFindChecksumHandlesAsteriskFormat(t *testing.T) {
	sum, err := findChecksum([]byte("abc123 *mutapod_1.2.3_windows_amd64.zip\n"), "mutapod_1.2.3_windows_amd64.zip")
	if err != nil {
		t.Fatalf("findChecksum: %v", err)
	}
	if sum != "abc123" {
		t.Fatalf("unexpected checksum: %s", sum)
	}
}

func TestWindowsReplaceScriptEscapesPercent(t *testing.T) {
	script := windowsReplaceScript(`C:\Temp\100%\new.exe`, `C:\Apps\100%\mutapod.exe`)
	if strings.Count(script, "%%") < 2 {
		t.Fatalf("expected percent signs to be escaped: %q", script)
	}
}

func makeZipArchive(t *testing.T, name string, contents []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	file, err := zw.Create(name)
	if err != nil {
		t.Fatalf("Create zip entry: %v", err)
	}
	if _, err := file.Write(contents); err != nil {
		t.Fatalf("Write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip: %v", err)
	}
	return buf.Bytes()
}

func makeTarGzArchive(t *testing.T, name string, contents []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0755,
		Size: int64(len(contents)),
	}); err != nil {
		t.Fatalf("Write tar header: %v", err)
	}
	if _, err := tw.Write(contents); err != nil {
		t.Fatalf("Write tar entry: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("Close gzip: %v", err)
	}
	return buf.Bytes()
}

func serverURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}
