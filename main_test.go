package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/rubiin/ignit/internal/gitignore"
)

// commandRecorder records which root action ran and what it returned.
type commandRecorder struct {
	pickerCalls int
	pickerErr   error
	updateCalls int
	updateErr   error
}

// newTestCmd builds the ignit command with stubbed seams so cmd.Run drives
// the full CLI stack (flag parsing, flag Actions, root action) without the
// TUI or the network.
func newTestCmd(t *testing.T, rec *commandRecorder) *cli.Command {
	t.Helper()

	// Stub the seams and restore them after the test.
	originalPicker, originalUpdate := runPickerFunc, updateListFunc
	runPickerFunc = func() error {
		rec.pickerCalls++
		return rec.pickerErr
	}
	updateListFunc = func() error {
		rec.updateCalls++
		return rec.updateErr
	}

	// Keep cache off the real user directory for every test.
	originalCacheDir, originalBypass := gitignore.CacheDir, gitignore.BypassCache
	gitignore.CacheDir = t.TempDir()
	gitignore.BypassCache = false
	t.Cleanup(func() {
		runPickerFunc, updateListFunc = originalPicker, originalUpdate
		gitignore.CacheDir = originalCacheDir
		gitignore.BypassCache = originalBypass
	})

	return newCommand()
}

// captureStdout runs fn with os.Stdout redirected and returns what was
// printed (for runRoot's plain fmt.Printf output).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w
	fn()
	os.Stdout = original
	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read captured stdout: %v", err)
	}
	return string(out)
}

func runCLI(t *testing.T, args ...string) error {
	t.Helper()
	cmd := newTestCmd(t, &commandRecorder{})
	return cmd.Run(context.Background(), append([]string{"ignit"}, args...))
}

func runCLIWithRecorder(t *testing.T, rec *commandRecorder, args ...string) error {
	t.Helper()
	cmd := newTestCmd(t, rec)
	return cmd.Run(context.Background(), append([]string{"ignit"}, args...))
}

func TestCLIDefaultRunsPicker(t *testing.T) {
	rec := &commandRecorder{}
	if err := runCLIWithRecorder(t, rec); err != nil {
		t.Fatalf("cmd.Run() error = %v", err)
	}
	if rec.pickerCalls != 1 {
		t.Errorf("picker ran %d times, want 1", rec.pickerCalls)
	}
	if rec.updateCalls != 0 {
		t.Errorf("update-list ran %d times, want 0", rec.updateCalls)
	}
}

func TestCLINoCacheSetsBypass(t *testing.T) {
	if gitignore.BypassCache {
		t.Fatal("BypassCache should start false")
	}
	if err := runCLI(t, "--no-cache"); err != nil {
		t.Fatalf("cmd.Run() error = %v", err)
	}
	if !gitignore.BypassCache {
		t.Error("--no-cache did not set gitignore.BypassCache")
	}
}

func TestCLIClearCache(t *testing.T) {
	rec := &commandRecorder{}
	var printed string
	runErr := func() error {
		var err error
		printed = captureStdout(t, func() {
			err = runCLIWithRecorder(t, rec, "--clear-cache=true")
		})
		return err
	}()
	if runErr != nil {
		t.Fatalf("cmd.Run() error = %v", runErr)
	}
	if !strings.Contains(printed, "cleared template cache at") {
		t.Errorf("output %q missing cache confirmation", printed)
	}
	if rec.pickerCalls != 0 {
		t.Errorf("picker ran %d times after --clear-cache, want 0", rec.pickerCalls)
	}
}

func TestCLIUpdateList(t *testing.T) {
	rec := &commandRecorder{updateErr: errors.New("boom")}
	if err := runCLIWithRecorder(t, rec, "--update-list"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("cmd.Run() error = %v, want boom", err)
	}
	if rec.updateCalls != 1 {
		t.Errorf("update-list ran %d times, want 1", rec.updateCalls)
	}
	if rec.pickerCalls != 0 {
		t.Errorf("picker ran %d times after --update-list, want 0", rec.pickerCalls)
	}
}

func TestCLIVersion(t *testing.T) {
	err := runCLI(t, "--version")
	if err == nil {
		return // zero exit code surfaces as nil in some cli versions
	}
	var exitErr cli.ExitCoder
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 0 {
		t.Errorf("cmd.Run(--version) error = %v, want exit code 0", err)
	}
}

func TestCLIUnknownFlagErrors(t *testing.T) {
	if err := runCLI(t, "--definitely-not-a-flag"); err == nil {
		t.Fatal("cmd.Run() expected error for unknown flag, got nil")
	}
}

func TestCLIFlagsCompose(t *testing.T) {
	// --no-cache applies even when --clear-cache short-circuits the picker.
	rec := &commandRecorder{}
	var printed string
	runErr := func() error {
		var err error
		printed = captureStdout(t, func() {
			err = runCLIWithRecorder(t, rec, "--no-cache", "--clear-cache=true")
		})
		return err
	}()
	if runErr != nil {
		t.Fatalf("cmd.Run() error = %v", runErr)
	}
	if !strings.Contains(printed, "cleared template cache at") {
		t.Errorf("output %q missing cache confirmation", printed)
	}
	if !gitignore.BypassCache {
		t.Error("--no-cache did not set gitignore.BypassCache when composed with --clear-cache")
	}
	if rec.pickerCalls != 0 {
		t.Errorf("picker ran %d times, want 0", rec.pickerCalls)
	}
}
