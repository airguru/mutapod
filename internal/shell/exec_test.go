package shell

import (
	"context"
	"errors"
	"testing"
)

func TestFakeCommander_CalledWith(t *testing.T) {
	f := NewFakeCommander()
	ctx := context.Background()
	_ = f.Run(ctx, RunOptions{}, "gcloud", "compute", "instances", "describe", "my-vm")

	if !f.CalledWith("gcloud", "compute", "instances", "describe", "my-vm") {
		t.Error("expected CalledWith to return true")
	}
	if f.CalledWith("gcloud", "compute", "instances", "delete", "my-vm") {
		t.Error("expected CalledWith to return false for different args")
	}
}

func TestFakeCommander_Stub(t *testing.T) {
	f := NewFakeCommander()
	f.Stub(`{"status":"RUNNING"}`, "gcloud", "compute", "instances", "describe", "my-vm")

	out, err := f.Output(context.Background(), RunOptions{}, "gcloud", "compute", "instances", "describe", "my-vm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != `{"status":"RUNNING"}` {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestFakeCommander_StubErr(t *testing.T) {
	f := NewFakeCommander()
	want := errors.New("instance not found")
	f.StubErr(want, "gcloud", "compute", "instances", "describe", "missing-vm")

	err := f.Run(context.Background(), RunOptions{}, "gcloud", "compute", "instances", "describe", "missing-vm")
	if !errors.Is(err, want) {
		t.Errorf("expected %v, got %v", want, err)
	}
}

func TestFakeCommander_CallCount(t *testing.T) {
	f := NewFakeCommander()
	ctx := context.Background()
	_ = f.Run(ctx, RunOptions{}, "gcloud", "a")
	_ = f.Run(ctx, RunOptions{}, "gcloud", "b")
	_ = f.Run(ctx, RunOptions{}, "az", "c")

	if got := f.CallCount("gcloud"); got != 2 {
		t.Errorf("expected 2 gcloud calls, got %d", got)
	}
	if got := f.CallCount("az"); got != 1 {
		t.Errorf("expected 1 az call, got %d", got)
	}
}

func TestRealCommander_Run(t *testing.T) {
	r := &Real{}
	// Use "go version" — guaranteed to exist since we're running Go tests
	err := r.Run(context.Background(), RunOptions{}, "go", "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRealCommander_Output(t *testing.T) {
	r := &Real{}
	out, err := r.Output(context.Background(), RunOptions{}, "go", "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsBytes(out, []byte("go version")) {
		t.Errorf("unexpected output: %q", out)
	}
}

func containsBytes(b, sub []byte) bool {
	if len(sub) > len(b) {
		return false
	}
	for i := 0; i <= len(b)-len(sub); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return true
		}
	}
	return false
}
