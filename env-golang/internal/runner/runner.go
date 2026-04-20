package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Result struct {
	Stdout     string
	Stderr     string
	ExitCode   int
	DurationMS int64
	TimedOut   bool
}

// Run writes the given Go source to a fresh temp directory, runs `go run`
// against it with a timeout, captures stdout/stderr, and cleans up.
func Run(parent context.Context, code string, timeout time.Duration) (Result, error) {
	if code == "" {
		return Result{}, errors.New("code is empty")
	}
	dir, err := os.MkdirTemp("", "gorun-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)

	mainPath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainPath, []byte(code), 0o644); err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", mainPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOFLAGS=-mod=mod",
		"GOCACHE="+filepath.Join(dir, ".cache"),
		"GOMODCACHE="+filepath.Join(dir, ".modcache"),
	)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	res := Result{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMS: dur.Milliseconds(),
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		if res.Stderr == "" {
			res.Stderr = "execution timed out"
		}
		return res, nil
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		res.ExitCode = -1
		if res.Stderr == "" {
			res.Stderr = runErr.Error()
		}
		return res, nil
	}
	res.ExitCode = 0
	return res, nil
}
