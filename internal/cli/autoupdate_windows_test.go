//go:build windows

package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRelaunchPreservesArguments(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	stagedExecutable := filepath.Join(tempDir, ".mutapod.exe.123.new")
	copyTestExecutable(t, executable, stagedExecutable)
	resultPath := filepath.Join(tempDir, "args.json")
	t.Setenv("MUTAPOD_TEST_RELAUNCH_HELPER", resultPath)
	want := []string{"argument with spaces", "a&b", "100%", `quoted "value"`}
	args := append([]string{"-test.run=TestRelaunchHelperProcess", "--"}, want...)

	exitCode, err := relaunch(stagedExecutable, args)
	if err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0", exitCode)
	}

	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("relaunched args: got %#v, want %#v", got, want)
	}
}

func copyTestExecutable(t *testing.T, source, target string) {
	t.Helper()
	src, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRelaunchHelperProcess(t *testing.T) {
	resultPath := os.Getenv("MUTAPOD_TEST_RELAUNCH_HELPER")
	if resultPath == "" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		os.Exit(2)
	}
	data, err := json.Marshal(os.Args[separator+1:])
	if err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(resultPath, data, 0600); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}
