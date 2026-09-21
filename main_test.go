package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rubiin/ignit/internal/gitignore"
)

// commandRecorder records which root action ran and what it returned.
type commandRecorder struct {
	pickerCalls int
	pickerErr   error
	updateCalls int
	updateErr   error
}

// newTestCmd builds the ignit command with stubbed seams so cmd.Execute
// drives the full CLI stack (flag parsing, runRoot dispatch) without the
// TUI or the network.
func newTestCmd(t *testing.T, rec *commandRecorder) *cobra.Command {
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

	return newRootCmd()
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
	cmd.SetArgs(args)
	return cmd.Execute()
}

func runCLIWithRecorder(t *testing.T, rec *commandRecorder, args ...string) error {
	t.Helper()
	cmd := newTestCmd(t, rec)
	cmd.SetArgs(args)
	return cmd.Execute()
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
	runVersion := func(args ...string) (string, error) {
		var printed string
		err := func() error {
			var err error
			printed = captureStdout(t, func() {
				err = runCLI(t, args...)
			})
			return err
		}()
		return printed, err
	}

	printed, err := runVersion("--version")
	if err != nil {
		t.Fatalf("cmd.Execute(--version) error = %v, want nil", err)
	}
	if !strings.Contains(printed, "version") {
		t.Errorf("--version output %q missing version line", printed)
	}

	printed, err = runVersion("-v")
	if err != nil {
		t.Fatalf("cmd.Execute(-v) error = %v, want nil", err)
	}
	if !strings.Contains(printed, "version") {
		t.Errorf("-v output %q missing version line", printed)
	}
}

func TestCLIUnknownFlagErrors(t *testing.T) {
	if err := runCLI(t, "--definitely-not-a-flag"); err == nil {
		t.Fatal("cmd.Execute() expected error for unknown flag, got nil")
	}
}

func TestCLICompletion(t *testing.T) {
	// Cobra's built-in completion subcommand backs the README examples.
	if err := runCLI(t, "completion", "bash"); err != nil {
		t.Fatalf("cmd.Execute(completion bash) error = %v", err)
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

// inWorkDir runs fn in dir, then restores the previous directory.
func inWorkDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("failed to restore working directory: %v", err)
		}
	}()
	fn()
}

func TestBackupGitignore(t *testing.T) {
	inWorkDir(t, t.TempDir(), func() {
		// No existing file: no backup, no error.
		backedUp, err := backupGitignore()
		if err != nil || backedUp {
			t.Errorf("backupGitignore() with no .gitignore = (%v, %v), want (false, nil)", backedUp, err)
		}

		// Existing file gets copied to .gitignore.bak.
		if err := os.WriteFile(".gitignore", []byte("*.log\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		backedUp, err = backupGitignore()
		if err != nil || !backedUp {
			t.Fatalf("backupGitignore() = (%v, %v), want (true, nil)", backedUp, err)
		}
		bak, err := os.ReadFile(".gitignore.bak")
		if err != nil {
			t.Fatalf("failed to read backup: %v", err)
		}
		if string(bak) != "*.log\n" {
			t.Errorf("backup content = %q, want %q", bak, "*.log\n")
		}
	})
}

func TestBackupGitignoreOverwritesStaleBackup(t *testing.T) {
	inWorkDir(t, t.TempDir(), func() {
		if err := os.WriteFile(".gitignore", []byte("new\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if err := os.WriteFile(".gitignore.bak", []byte("old\n"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		backedUp, err := backupGitignore()
		if err != nil || !backedUp {
			t.Fatalf("backupGitignore() = (%v, %v), want (true, nil)", backedUp, err)
		}
		bak, err := os.ReadFile(".gitignore.bak")
		if err != nil {
			t.Fatalf("failed to read backup: %v", err)
		}
		if string(bak) != "new\n" {
			t.Errorf("backup = %q, want the current .gitignore contents (%q)", bak, "new\n")
		}
	})
}

func TestBackupGitignoreUnreadableFile(t *testing.T) {
	inWorkDir(t, t.TempDir(), func() {
		if err := os.WriteFile(".gitignore", []byte("x"), 0o000); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if _, err := backupGitignore(); err == nil {
			t.Fatal("backupGitignore() expected error for unreadable .gitignore, got nil")
		}
	})
}

func TestDownloadingModelShowsBackupNote(t *testing.T) {
	m := newDownloadingModel([]string{"go"})

	// The backup notice event flips the flag...
	updated, _ := m.Update(fetchEvent{backedUp: true})
	m = updated.(downloadingModel)
	if !m.backedUp {
		t.Fatal("backedUp flag not set by backup fetchEvent")
	}

	// ...and the success view mentions the backup file.
	updated, _ = m.Update(fetchEvent{done: true})
	m = updated.(downloadingModel)
	if view := m.View(); !strings.Contains(view, backupPath) {
		t.Errorf("success view should mention %s, got:\n%s", backupPath, view)
	}
}

func TestDownloadingModelOmitsBackupNoteWhenFresh(t *testing.T) {
	m := newDownloadingModel([]string{"go"})
	updated, _ := m.Update(fetchEvent{done: true})
	m = updated.(downloadingModel)
	if view := m.View(); strings.Contains(view, backupPath) {
		t.Errorf("success view should not mention a backup when none was made:\n%s", view)
	}
}
