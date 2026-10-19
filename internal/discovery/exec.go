package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Command execution and file reading helpers shared by every collector.
//
// Safety rules that this file exists to enforce:
//
//   - Commands are always built with exec.CommandContext and an explicit argv
//     slice. No argv element is ever produced by concatenating untrusted input
//     into a command string, and no shell is involved.
//   - Every command runs under a context, so a hung binary cannot wedge the
//     discovery loop.
//   - Output is capped. A pathological dump (or a device that floods stdout)
//     must not exhaust the router's memory.
//   - A missing binary, a missing file, an unsupported platform or a permission
//     error are all normal conditions. They make the collector unsupported, not
//     broken.

// DefaultCommandTimeout bounds a single system command. Router class hardware
// is slow, so this is generous enough for a busy bridge fdb dump but short
// enough that discovery never stalls the API.
const DefaultCommandTimeout = 5 * time.Second

// DefaultMaxOutput caps how much of a command's output is retained.
const DefaultMaxOutput = 4 << 20 // 4 MiB

// DefaultMaxFileSize caps how much of a file is read into memory.
const DefaultMaxFileSize = 4 << 20 // 4 MiB

// ErrNotSupported marks a helper whose input is unavailable. Collectors map it
// to Supported() == false; it is never surfaced as a discovery error.
var ErrNotSupported = errors.New("discovery: source not supported on this host")

// CommandRunner executes a command and returns its combined output. It is a
// seam so tests can substitute a fake runner without touching the real system.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// FileReader reads a file with a size cap. It is the seam the collectors use so
// tests can feed them fixtures instead of real router files.
type FileReader func(path string, max int) (data []byte, supported bool, err error)

// DefaultFileReader is the production FileReader: it reads from the filesystem.
func DefaultFileReader(path string, max int) ([]byte, bool, error) {
	return ReadFileCapped(path, max)
}

// ExecConfig tunes RunCommand and friends.
type ExecConfig struct {
	// Timeout bounds the command. Zero uses DefaultCommandTimeout.
	Timeout time.Duration
	// MaxOutput caps retained output. Zero uses DefaultMaxOutput.
	MaxOutput int
	// Dir optionally sets the working directory.
	Dir string
	// Env optionally replaces the environment (nil inherits the parent).
	Env []string
}

// RunCommand executes name with args under ctx, returning combined stdout and
// stderr truncated to cfg.MaxOutput.
//
// The error is wrapped with the command that failed so callers can log it
// without re-assembling the argv.
func RunCommand(ctx context.Context, cfg ExecConfig, name string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	if cfg.MaxOutput <= 0 {
		cfg.MaxOutput = DefaultMaxOutput
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cfg.Dir
	if cfg.Env != nil {
		cmd.Env = cfg.Env
	}
	// The command never reads stdin: a collector that accidentally waits for
	// input would hang until the timeout instead of failing fast.
	cmd.Stdin = strings.NewReader("")

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// Non-zero exit is a legitimate outcome (a busybox `ip` on an
			// unsupported family, for example). Return the partial output so
			// callers can still parse what was printed.
			return truncate(out, cfg.MaxOutput), fmt.Errorf("discovery: %s %s: %w",
				name, strings.Join(args, " "), err)
		}
		if errors.Is(err, exec.ErrNotFound) || isNotFound(err) {
			return nil, fmt.Errorf("discovery: %s: %w", name, ErrNotSupported)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("discovery: %s %s: %w",
				name, strings.Join(args, " "), ctx.Err())
		}
		return nil, fmt.Errorf("discovery: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return truncate(out, cfg.MaxOutput), nil
}

// isNotFound reports whether err indicates the executable is missing.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found") ||
		strings.Contains(err.Error(), "no such file or directory")
}

// truncate caps a byte slice, appending an ellipsis marker when it cut.
func truncate(b []byte, max int) []byte {
	if max <= 0 || len(b) <= max {
		return b
	}
	out := make([]byte, 0, max+16)
	out = append(out, b[:max]...)
	out = append(out, []byte("\n…[truncated]")...)
	return out
}

// LookPath reports whether name resolves to an executable in PATH. It is used
// by Supported() so an unsupported collector is reported as such instead of
// failing on first run.
func LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// FileStat reports whether path exists, is a regular file and how large it is.
type FileStat struct {
	Exists bool
	IsDir  bool
	Size   int64
}

// StatFile returns the status of path. A non-existent path yields
// Exists == false and a nil error, which is how "no lease file yet" is
// distinguished from "cannot read the lease file".
func StatFile(path string) (FileStat, error) {
	if strings.TrimSpace(path) == "" {
		return FileStat{}, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileStat{}, nil
		}
		return FileStat{}, fmt.Errorf("discovery: stat %s: %w", path, err)
	}
	return FileStat{Exists: true, IsDir: fi.IsDir(), Size: fi.Size()}, nil
}

// ReadFileCapped reads at most max bytes from path.
//
// The boolean reports whether the source is usable:
//
//	false, nil   -> the path is empty, missing or is a directory: not supported
//	false, err   -> the path exists but cannot be read: a real error
//	true,  nil   -> data was read
//
// Callers must not treat "not supported" as a discovery failure.
func ReadFileCapped(path string, max int) ([]byte, bool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, false, nil
	}
	st, err := StatFile(path)
	if err != nil {
		return nil, false, err
	}
	if !st.Exists || st.IsDir {
		return nil, false, nil
	}
	if max <= 0 {
		max = DefaultMaxFileSize
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if os.IsPermission(err) {
			return nil, false, fmt.Errorf("discovery: read %s: %w", path, err)
		}
		return nil, false, fmt.Errorf("discovery: read %s: %w", path, err)
	}
	defer f.Close()
	// One extra byte so the caller can detect that the cap was hit.
	buf := make([]byte, max+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, fmt.Errorf("discovery: read %s: %w", path, err)
	}
	if n > max {
		return buf[:max], true, nil
	}
	return buf[:n], true, nil
}

// ReadFileCappedDefault reads a file with DefaultMaxFileSize.
func ReadFileCappedDefault(path string) ([]byte, bool, error) {
	return ReadFileCapped(path, DefaultMaxFileSize)
}

// FirstNonEmpty returns the first non-blank argument, or "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Lines splits raw output into non-empty lines.
func Lines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	raw := strings.Split(string(b), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// CountLines reports the number of non-empty lines, used for quick sanity
// checks in logs and tests.
func CountLines(b []byte) int { return len(Lines(b)) }

// JoinLines is a small helper for building evidence strings.
func JoinLines(b []byte, max int) string {
	lines := Lines(b)
	if max > 0 && len(lines) > max {
		lines = append(lines[:max], fmt.Sprintf("… (%d more lines)", len(lines)-max))
	}
	return strings.Join(lines, "\n")
}

var _ = bytes.MinRead
