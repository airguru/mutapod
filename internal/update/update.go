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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	repoOwner         = "airguru"
	repoName          = "mutapod"
	checksumAssetName = "checksums.txt"
	projectName       = "mutapod"
)

var startDetachedWindowsReplace = func(scriptPath string) error {
	cmd := exec.Command("cmd.exe", "/C", "start", "", "/B", scriptPath)
	return cmd.Start()
}

type Updater struct {
	HTTPClient     *http.Client
	APIBaseURL     string
	GOOS           string
	GOARCH         string
	ExecutablePath string
}

type Release struct {
	TagName      string
	Version      string
	PublishedAt  time.Time
	AssetName    string
	AssetURL     string
	ChecksumURL  string
	DownloadPage string
}

type Status struct {
	CurrentVersion      string
	CurrentVersionKnown bool
	UpToDate            bool
	Latest              Release
}

type Result struct {
	Release        Release
	Updated        bool
	PendingRestart bool
	ExecutablePath string
}

type githubRelease struct {
	HTMLURL     string    `json:"html_url"`
	TagName     string    `json:"tag_name"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func New() (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("update: resolve current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	return &Updater{
		HTTPClient:     &http.Client{Timeout: 30 * time.Second},
		APIBaseURL:     defaultAPIBaseURL,
		GOOS:           runtime.GOOS,
		GOARCH:         runtime.GOARCH,
		ExecutablePath: exe,
	}, nil
}

func (u *Updater) Check(ctx context.Context, currentVersion string) (*Status, error) {
	release, err := u.latestRelease(ctx)
	if err != nil {
		return nil, err
	}

	currentKnown := isReleaseVersion(currentVersion)
	upToDate := false
	if currentKnown {
		upToDate = compareVersions(currentVersion, release.Version) >= 0
	}

	return &Status{
		CurrentVersion:      currentVersion,
		CurrentVersionKnown: currentKnown,
		UpToDate:            upToDate,
		Latest:              release,
	}, nil
}

func (u *Updater) Update(ctx context.Context, currentVersion string) (*Result, error) {
	status, err := u.Check(ctx, currentVersion)
	if err != nil {
		return nil, err
	}
	if status.UpToDate {
		return &Result{
			Release:        status.Latest,
			Updated:        false,
			ExecutablePath: u.ExecutablePath,
		}, nil
	}

	archiveData, err := u.download(ctx, status.Latest.AssetURL)
	if err != nil {
		return nil, err
	}
	checksumData, err := u.download(ctx, status.Latest.ChecksumURL)
	if err != nil {
		return nil, err
	}

	expectedChecksum, err := findChecksum(checksumData, status.Latest.AssetName)
	if err != nil {
		return nil, err
	}
	actualHash := sha256.Sum256(archiveData)
	actualChecksum := hex.EncodeToString(actualHash[:])
	if !strings.EqualFold(actualChecksum, expectedChecksum) {
		return nil, fmt.Errorf("update: checksum mismatch for %s", status.Latest.AssetName)
	}

	binaryPath, cleanup, err := extractBinary(archiveData, status.Latest.AssetName, u.GOOS)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	pendingRestart, err := installBinary(binaryPath, u.ExecutablePath, u.GOOS)
	if err != nil {
		return nil, err
	}

	return &Result{
		Release:        status.Latest,
		Updated:        true,
		PendingRestart: pendingRestart,
		ExecutablePath: u.ExecutablePath,
	}, nil
}

func (u *Updater) latestRelease(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(u.APIBaseURL, "/"), repoOwner, repoName)
	data, err := u.download(ctx, url)
	if err != nil {
		return Release{}, err
	}

	var payload githubRelease
	if err := json.Unmarshal(data, &payload); err != nil {
		return Release{}, fmt.Errorf("update: parse latest release metadata: %w", err)
	}

	version := normalizeVersion(payload.TagName)
	assetName, err := archiveNameFor(version, u.GOOS, u.GOARCH)
	if err != nil {
		return Release{}, err
	}

	assetURL := ""
	checksumURL := ""
	for _, asset := range payload.Assets {
		switch asset.Name {
		case assetName:
			assetURL = asset.BrowserDownloadURL
		case checksumAssetName:
			checksumURL = asset.BrowserDownloadURL
		}
	}
	if assetURL == "" {
		return Release{}, fmt.Errorf("update: release %s does not include %s", payload.TagName, assetName)
	}
	if checksumURL == "" {
		return Release{}, fmt.Errorf("update: release %s does not include %s", payload.TagName, checksumAssetName)
	}

	return Release{
		TagName:      payload.TagName,
		Version:      version,
		PublishedAt:  payload.PublishedAt,
		AssetName:    assetName,
		AssetURL:     assetURL,
		ChecksumURL:  checksumURL,
		DownloadPage: payload.HTMLURL,
	}, nil
}

func (u *Updater) download(ctx context.Context, url string) ([]byte, error) {
	client := u.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: build request for %s: %w", url, err)
	}
	req.Header.Set("User-Agent", "mutapod-updater")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if len(body) > 0 {
			return nil, fmt.Errorf("update: download %s: HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("update: download %s: HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("update: read %s: %w", url, err)
	}
	return data, nil
}

func archiveNameFor(version, goos, goarch string) (string, error) {
	version = normalizeVersion(version)
	switch goos {
	case "windows":
		if goarch != "amd64" {
			return "", fmt.Errorf("update: unsupported platform %s/%s", goos, goarch)
		}
		return fmt.Sprintf("%s_%s_%s_%s.zip", projectName, version, goos, goarch), nil
	case "linux", "darwin":
		if goarch != "amd64" && goarch != "arm64" {
			return "", fmt.Errorf("update: unsupported platform %s/%s", goos, goarch)
		}
		return fmt.Sprintf("%s_%s_%s_%s.tar.gz", projectName, version, goos, goarch), nil
	default:
		return "", fmt.Errorf("update: unsupported platform %s/%s", goos, goarch)
	}
}

func binaryName(goos string) string {
	if goos == "windows" {
		return projectName + ".exe"
	}
	return projectName
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

type parsedVersion struct {
	parts [3]int
	pre   string
}

func parseVersion(v string) (parsedVersion, bool) {
	v = normalizeVersion(v)
	if v == "" {
		return parsedVersion{}, false
	}

	var result parsedVersion
	mainPart := v
	if idx := strings.Index(mainPart, "-"); idx >= 0 {
		result.pre = mainPart[idx+1:]
		mainPart = mainPart[:idx]
	}

	segments := strings.Split(mainPart, ".")
	if len(segments) == 0 || len(segments) > 3 {
		return parsedVersion{}, false
	}
	for i, segment := range segments {
		if segment == "" {
			return parsedVersion{}, false
		}
		value, err := strconv.Atoi(segment)
		if err != nil {
			return parsedVersion{}, false
		}
		result.parts[i] = value
	}
	return result, true
}

func isReleaseVersion(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

func compareVersions(a, b string) int {
	aVersion, aOK := parseVersion(a)
	bVersion, bOK := parseVersion(b)
	switch {
	case !aOK && !bOK:
		return 0
	case !aOK:
		return -1
	case !bOK:
		return 1
	}

	for i := 0; i < 3; i++ {
		switch {
		case aVersion.parts[i] < bVersion.parts[i]:
			return -1
		case aVersion.parts[i] > bVersion.parts[i]:
			return 1
		}
	}

	switch {
	case aVersion.pre == "" && bVersion.pre == "":
		return 0
	case aVersion.pre == "":
		return 1
	case bVersion.pre == "":
		return -1
	case aVersion.pre < bVersion.pre:
		return -1
	case aVersion.pre > bVersion.pre:
		return 1
	default:
		return 0
	}
}

func findChecksum(data []byte, assetName string) (string, error) {
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == assetName {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("update: checksum for %s not found in %s", assetName, checksumAssetName)
}

func extractBinary(archiveData []byte, archiveName, goos string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "mutapod-update-*")
	if err != nil {
		return "", nil, fmt.Errorf("update: create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	target := filepath.Join(tmpDir, binaryName(goos))
	if strings.HasSuffix(archiveName, ".zip") {
		if err := extractBinaryFromZip(archiveData, target, goos); err != nil {
			cleanup()
			return "", nil, err
		}
		return target, cleanup, nil
	}
	if strings.HasSuffix(archiveName, ".tar.gz") {
		if err := extractBinaryFromTarGz(archiveData, target, goos); err != nil {
			cleanup()
			return "", nil, err
		}
		return target, cleanup, nil
	}

	cleanup()
	return "", nil, fmt.Errorf("update: unsupported archive format for %s", archiveName)
}

func extractBinaryFromZip(data []byte, targetPath, goos string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("update: open zip archive: %w", err)
	}
	want := binaryName(goos)
	for _, file := range reader.File {
		if filepath.Base(file.Name) != want {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("update: open %s in archive: %w", want, err)
		}
		defer rc.Close()
		if err := writeExecutable(targetPath, rc, 0755); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("update: %s not found in archive", want)
}

func extractBinaryFromTarGz(data []byte, targetPath, goos string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("update: open gzip archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	want := binaryName(goos)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("update: read tar archive: %w", err)
		}
		if filepath.Base(header.Name) != want {
			continue
		}
		mode := os.FileMode(0755)
		if header.Mode != 0 {
			mode = os.FileMode(header.Mode)
		}
		if err := writeExecutable(targetPath, tr, mode); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("update: %s not found in archive", want)
}

func writeExecutable(path string, src io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("update: create %s: %w", path, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, src); err != nil {
		return fmt.Errorf("update: write %s: %w", path, err)
	}
	return nil
}

func installBinary(srcPath, targetPath, goos string) (bool, error) {
	if goos == "windows" {
		return true, stageWindowsReplacement(srcPath, targetPath)
	}
	return false, replaceExecutable(srcPath, targetPath)
}

func replaceExecutable(srcPath, targetPath string) error {
	mode := os.FileMode(0755)
	if info, err := os.Stat(targetPath); err == nil {
		mode = info.Mode()
	}

	tmpPath := filepath.Join(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".tmp")
	if err := copyFile(srcPath, tmpPath, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("update: replace %s: %w", targetPath, err)
	}
	return nil
}

func stageWindowsReplacement(srcPath, targetPath string) error {
	stagedPath := filepath.Join(filepath.Dir(targetPath), fmt.Sprintf(".%s.%d.new", filepath.Base(targetPath), time.Now().UnixNano()))
	if err := copyFile(srcPath, stagedPath, 0755); err != nil {
		return err
	}

	scriptPath, err := writeWindowsReplaceScript(stagedPath, targetPath)
	if err != nil {
		_ = os.Remove(stagedPath)
		return err
	}
	if err := startDetachedWindowsReplace(scriptPath); err != nil {
		_ = os.Remove(stagedPath)
		_ = os.Remove(scriptPath)
		return fmt.Errorf("update: launch Windows replace helper: %w", err)
	}
	return nil
}

func writeWindowsReplaceScript(sourcePath, targetPath string) (string, error) {
	file, err := os.CreateTemp("", "mutapod-replace-*.cmd")
	if err != nil {
		return "", fmt.Errorf("update: create Windows replace helper: %w", err)
	}
	defer file.Close()

	script := windowsReplaceScript(sourcePath, targetPath)
	if _, err := file.WriteString(script); err != nil {
		return "", fmt.Errorf("update: write Windows replace helper: %w", err)
	}
	return file.Name(), nil
}

func windowsReplaceScript(sourcePath, targetPath string) string {
	escape := func(s string) string {
		return strings.ReplaceAll(s, "%", "%%")
	}

	lines := []string{
		"@echo off",
		"setlocal",
		fmt.Sprintf(`set "SOURCE=%s"`, escape(sourcePath)),
		fmt.Sprintf(`set "TARGET=%s"`, escape(targetPath)),
		":retry",
		`move /Y "%SOURCE%" "%TARGET%" >NUL 2>NUL`,
		"if errorlevel 1 (",
		"  ping 127.0.0.1 -n 2 >NUL",
		"  goto retry",
		")",
		`del "%~f0" >NUL 2>NUL`,
		"",
	}
	return strings.Join(lines, "\r\n")
}

func copyFile(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("update: open %s: %w", srcPath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("update: create %s: %w", dstPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("update: copy %s to %s: %w", srcPath, dstPath, err)
	}
	return nil
}
