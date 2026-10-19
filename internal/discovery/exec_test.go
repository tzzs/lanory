package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommandCapturesOutput(t *testing.T) {
	out, err := RunCommand(context.Background(), ExecConfig{}, "echo", "hello", "world")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Fatalf("output = %q", out)
	}
}

// A command that does not exist must report ErrNotSupported, not a generic
// error, so callers can treat "no such tool" as a normal condition.
func TestRunCommandMissingBinary(t *testing.T) {
	_, err := RunCommand(context.Background(), ExecConfig{}, "lanory-no-such-binary-xyz")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("expected ErrNotSupported, got %v", err)
	}
}

// A non-zero exit must be surfaced, not swallowed: the collector may still want
// the partial output.
func TestRunCommandNonZeroExit(t *testing.T) {
	_, err := RunCommand(context.Background(), ExecConfig{}, "sh", "-c", "echo out; echo err >&2; exit 3")
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
}

// A hanging command must be killed by the context.
func TestRunCommandContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := RunCommand(ctx, ExecConfig{}, "sleep", "30")
	if err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("command was not cancelled, took %v", elapsed)
	}
}

// The output cap must be honoured: a dump larger than MaxOutput is truncated.
func TestRunCommandOutputCap(t *testing.T) {
	// 100 KiB of output, capped to 1 KiB.
	out, err := RunCommand(context.Background(), ExecConfig{MaxOutput: 1024}, "dd", "if=/dev/zero", "bs=1024", "count=100")
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if len(out) > 1024+64 {
		t.Fatalf("output not capped: %d bytes", len(out))
	}
	if !strings.Contains(string(out), "truncated") {
		t.Fatalf("expected a truncation marker, got %d bytes", len(out))
	}
}

func TestRunCommandTimeoutOverridesContext(t *testing.T) {
	// The ExecConfig timeout is applied inside RunCommand even when the parent
	// context has no deadline.
	start := time.Now()
	_, err := RunCommand(context.Background(), ExecConfig{Timeout: 150 * time.Millisecond}, "sleep", "30")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout not applied, took %v", elapsed)
	}
}

// The command must never be able to read stdin: a collector that accidentally
// waits for input must fail fast instead of hanging.
func TestRunCommandIgnoresStdin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := RunCommand(ctx, ExecConfig{}, "cat")
	// `cat` with no arguments and no stdin returns immediately with no output
	// because stdin is empty, rather than blocking forever.
	if err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no output, got %q", out)
	}
}

func TestLookPath(t *testing.T) {
	if !LookPath("echo") {
		t.Error("echo should be in PATH on a POSIX system")
	}
	if LookPath("lanory-no-such-binary-xyz") {
		t.Error("a non-existent binary must not be found")
	}
}

func TestStatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	st, err := StatFile(path)
	if err != nil {
		t.Fatalf("StatFile: %v", err)
	}
	if !st.Exists || st.IsDir || st.Size != 1 {
		t.Fatalf("stat = %+v", st)
	}

	// A missing file is not an error.
	st, err = StatFile(filepath.Join(dir, "absent"))
	if err != nil {
		t.Fatalf("StatFile(missing): %v", err)
	}
	if st.Exists {
		t.Fatal("missing file must report Exists=false")
	}

	// A directory exists but is not a usable file.
	st, err = StatFile(dir)
	if err != nil {
		t.Fatalf("StatFile(dir): %v", err)
	}
	if !st.Exists || !st.IsDir {
		t.Fatalf("directory stat = %+v", st)
	}

	// An empty path is not an error either.
	if st, err := StatFile(""); err != nil || st.Exists {
		t.Fatalf("empty path: %+v %v", st, err)
	}
}

func TestReadFileCapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, ok, err := ReadFileCapped(path, 0)
	if err != nil || !ok {
		t.Fatalf("read: %v ok=%v", err, ok)
	}
	if string(data) != "0123456789" {
		t.Fatalf("data = %q", data)
	}

	// A cap smaller than the file truncates.
	data, ok, err = ReadFileCapped(path, 4)
	if err != nil || !ok {
		t.Fatalf("read capped: %v ok=%v", err, ok)
	}
	if string(data) != "0123" {
		t.Fatalf("capped data = %q", data)
	}
}

func TestReadFileCappedMissing(t *testing.T) {
	_, ok, err := ReadFileCapped(filepath.Join(t.TempDir(), "absent"), 0)
	if err != nil {
		t.Fatalf("a missing file must not be an error: %v", err)
	}
	if ok {
		t.Fatal("a missing file must be unsupported")
	}
}

func TestReadFileCappedEmptyPath(t *testing.T) {
	data, ok, err := ReadFileCapped("", 0)
	if err != nil || ok || data != nil {
		t.Fatalf("empty path: data=%v ok=%v err=%v", data, ok, err)
	}
}

func TestReadFileCappedDirectory(t *testing.T) {
	_, ok, err := ReadFileCapped(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("a directory must not be an error: %v", err)
	}
	if ok {
		t.Fatal("a directory must be unsupported")
	}
}

func TestLines(t *testing.T) {
	got := Lines([]byte("a\r\n\nb\n  \nc\n"))
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if Lines(nil) != nil {
		t.Fatal("nil input must yield nil")
	}
}

func TestJoinLines(t *testing.T) {
	got := JoinLines([]byte("a\nb\nc\nd\n"), 2)
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "more lines") {
		t.Fatalf("expected a truncation note, got %q", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", "b", "c"); got != "b" {
		t.Fatalf("got %q", got)
	}
	if got := FirstNonEmpty("", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCollectError(t *testing.T) {
	inner := errors.New("boom")
	err := CollectError("arp", inner)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, inner) {
		t.Fatal("CollectError must wrap the original error")
	}
	if !strings.Contains(err.Error(), "arp") {
		t.Fatalf("error must name the collector: %v", err)
	}
	// A nil error stays nil.
	if CollectError("arp", nil) != nil {
		t.Fatal("nil must stay nil")
	}
}
