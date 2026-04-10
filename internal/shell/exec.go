package shell

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// DefaultContext returns context.Background(). Provided so other packages
// can call shell operations without importing "context" themselves.
func DefaultContext() context.Context { return context.Background() }

// DebugWriters returns stdout/stderr writers appropriate for the current mode.
// In debug mode they write to os.Stdout/os.Stderr; otherwise they discard.
func DebugWriters() (io.Writer, io.Writer) {
	if debugEnabled {
		return os.Stdout, os.Stderr
	}
	return io.Discard, io.Discard
}

var debugEnabled bool

// SetDebug enables or disables debug output for all shell operations.
func SetDebug(v bool) { debugEnabled = v }

// IsDebug returns whether debug mode is active.
func IsDebug() bool { return debugEnabled }

// Debugf prints a debug message to stderr when debug mode is enabled.
func Debugf(format string, args ...any) {
	if debugEnabled {
		fmt.Fprintf(os.Stderr, "[debug] "+format+"\n", args...)
	}
}

// RunOptions controls how a command is executed.
type RunOptions struct {
	// Stdin is optionally attached to the process stdin.
	Stdin io.Reader
	// Stdout overrides where stdout goes. Nil = captured (or streamed in debug mode).
	Stdout io.Writer
	// Stderr overrides where stderr goes. Nil = captured (or streamed in debug mode).
	Stderr io.Writer
	// Dir sets the working directory. Empty = inherit.
	Dir string
	// Env adds extra environment variables (KEY=VALUE format).
	Env []string
}

// Commander is the interface used to run external processes.
// Swap it out in tests with FakeCommander.
type Commander interface {
	Run(ctx context.Context, opts RunOptions, name string, args ...string) error
	Output(ctx context.Context, opts RunOptions, name string, args ...string) ([]byte, error)
}

// Real is the production Commander that runs actual OS processes.
type Real struct{}

// DefaultCommander is the package-level commander used by helpers.
var DefaultCommander Commander = &Real{}

// Run executes a command, returning an error that includes stderr if the command fails.
func (r *Real) Run(ctx context.Context, opts RunOptions, name string, args ...string) error {
	_, err := run(ctx, opts, false, name, args...)
	return err
}

// Output executes a command and returns its stdout.
func (r *Real) Output(ctx context.Context, opts RunOptions, name string, args ...string) ([]byte, error) {
	return run(ctx, opts, true, name, args...)
}

func run(ctx context.Context, opts RunOptions, captureOutput bool, name string, args ...string) ([]byte, error) {
	Debugf("exec: %s %s", name, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, name, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	var stdoutBuf, stderrBuf bytes.Buffer

	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else if debugEnabled {
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
	} else if captureOutput {
		cmd.Stdout = &stdoutBuf
	} else {
		cmd.Stdout = &stdoutBuf // capture for error context
	}

	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else if debugEnabled {
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	} else {
		cmd.Stderr = &stderrBuf
	}

	err := cmd.Run()
	if err != nil {
		if stderrBuf.Len() > 0 {
			return nil, fmt.Errorf("%w\n%s", err, strings.TrimSpace(stderrBuf.String()))
		}
		return nil, err
	}
	return stdoutBuf.Bytes(), nil
}

// Run is a package-level convenience wrapper around DefaultCommander.
func Run(ctx context.Context, name string, args ...string) error {
	return DefaultCommander.Run(ctx, RunOptions{}, name, args...)
}

// RunOpts is a package-level convenience wrapper with options.
func RunOpts(ctx context.Context, opts RunOptions, name string, args ...string) error {
	return DefaultCommander.Run(ctx, opts, name, args...)
}

// Output is a package-level convenience wrapper that returns stdout.
func Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return DefaultCommander.Output(ctx, RunOptions{}, name, args...)
}

// OutputOpts is a package-level convenience wrapper with options.
func OutputOpts(ctx context.Context, opts RunOptions, name string, args ...string) ([]byte, error) {
	return DefaultCommander.Output(ctx, opts, name, args...)
}

// OutputString returns stdout as a trimmed string.
func OutputString(ctx context.Context, name string, args ...string) (string, error) {
	b, err := Output(ctx, name, args...)
	return strings.TrimSpace(string(b)), err
}

// --- FakeCommander for tests ---

// Call records a single command invocation.
type Call struct {
	Name string
	Args []string
	Opts RunOptions
}

// FakeCommander records all invocations and returns pre-configured responses.
type FakeCommander struct {
	Calls []Call
	// Responses maps "name args[0] args[1]..." to stdout bytes.
	Responses map[string][]byte
	// Errors maps "name args[0] args[1]..." to errors.
	Errors map[string]error
}

func NewFakeCommander() *FakeCommander {
	return &FakeCommander{
		Responses: make(map[string][]byte),
		Errors:    make(map[string]error),
	}
}

func (f *FakeCommander) key(name string, args []string) string {
	parts := append([]string{name}, args...)
	return strings.Join(parts, " ")
}

// Stub registers a pre-configured stdout response for a command.
func (f *FakeCommander) Stub(stdout string, name string, args ...string) {
	f.Responses[f.key(name, args)] = []byte(stdout)
}

// StubErr registers a pre-configured error for a command.
func (f *FakeCommander) StubErr(err error, name string, args ...string) {
	f.Errors[f.key(name, args)] = err
}

func (f *FakeCommander) Run(ctx context.Context, opts RunOptions, name string, args ...string) error {
	f.Calls = append(f.Calls, Call{Name: name, Args: args, Opts: opts})
	k := f.key(name, args)
	if err, ok := f.Errors[k]; ok {
		return err
	}
	return nil
}

func (f *FakeCommander) Output(ctx context.Context, opts RunOptions, name string, args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, Call{Name: name, Args: args, Opts: opts})
	k := f.key(name, args)
	if err, ok := f.Errors[k]; ok {
		return nil, err
	}
	if out, ok := f.Responses[k]; ok {
		return out, nil
	}
	return nil, nil
}

// CalledWith returns true if the commander was called with the given name and args.
func (f *FakeCommander) CalledWith(name string, args ...string) bool {
	for _, c := range f.Calls {
		if c.Name == name && argsMatch(c.Args, args) {
			return true
		}
	}
	return false
}

// CallCount returns how many times a command was called.
func (f *FakeCommander) CallCount(name string) int {
	n := 0
	for _, c := range f.Calls {
		if c.Name == name {
			n++
		}
	}
	return n
}

func argsMatch(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
