// Command ignit is a CLI app that quickly generates .gitignore files.
//
// The root package is wiring only: flag handling, the picker/download
// program flow, and writing the merged .gitignore. The picker lives in
// internal/picker, the template fetching/merging in internal/gitignore, the
// list generator in internal/envlist, and the embedded environment snapshot
// in internal/envs.
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/rubiin/ignit/internal/envlist"
	"github.com/rubiin/ignit/internal/envs"
	"github.com/rubiin/ignit/internal/gitignore"
	"github.com/rubiin/ignit/internal/picker"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// backupPath is where the previous .gitignore is kept when overwritten.
const backupPath = ".gitignore.bak"

// backupGitignore copies the current .gitignore to .gitignore.bak before it
// is overwritten. A missing file is not an error.
func backupGitignore() (bool, error) {
	original, err := os.ReadFile(".gitignore")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not read existing .gitignore: %w", err)
	}
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		return false, fmt.Errorf("could not write %s: %w", backupPath, err)
	}
	return true, nil
}

// writeGitignore backs up any existing .gitignore, merges the template for
// each language client-side, and writes the result to .gitignore in the
// current directory, streaming one event per completed fetch into progress.
// The channel must be buffered with room for every fetch so the callback
// never blocks.
func writeGitignore(languages []string, progress chan<- fetchEvent) error {
	backedUp, err := backupGitignore()
	if err != nil {
		return err
	}

	content, err := gitignore.MergeWithProgress(languages, func(language string, source gitignore.Source) {
		progress <- fetchEvent{language: language, source: source}
	})
	if err != nil {
		return err
	}
	if err := gitignore.Write(".gitignore", content); err != nil {
		return err
	}
	if backedUp {
		progress <- fetchEvent{backedUp: true}
	}
	return nil
}

// fetchEvent is one progress update from the download goroutine: either a
// template fetch completing (with its origin), the backup notice, or the
// final result.
type fetchEvent struct {
	language string
	source   gitignore.Source
	backedUp bool
	done     bool
	err      error
}

// fetchProgressMsg hands the model the event channel to start listening on.
type fetchProgressMsg struct {
	ch <-chan fetchEvent
}

func writeFileCmd(languages []string) tea.Cmd {
	return func() tea.Msg {
		// Buffered so the download goroutine never blocks even if the
		// program quits before draining it.
		ch := make(chan fetchEvent, len(languages)+1)
		go func() {
			err := writeGitignore(languages, ch)
			ch <- fetchEvent{done: true, err: err}
			close(ch)
		}()
		return fetchProgressMsg{ch: ch}
	}
}

// listenFetchCmd waits for the next progress event and delivers it to the
// model.
func listenFetchCmd(ch <-chan fetchEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return fetchEvent{done: true}
		}
		return event
	}
}

// downloadingModel shows a spinner with per-template progress while the
// .gitignore downloads, then a success screen (any key exits) or the error.
type downloadingModel struct {
	spinner   spinner.Model
	languages []string
	sources   map[string]gitignore.Source
	ch        <-chan fetchEvent
	err       error
	done      bool
	backedUp  bool
}

func newDownloadingModel(languages []string) downloadingModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return downloadingModel{spinner: s, languages: languages, sources: make(map[string]gitignore.Source)}
}

func (m downloadingModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, writeFileCmd(m.languages))
}

func (m downloadingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.done || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case fetchProgressMsg:
		m.ch = msg.ch
		return m, listenFetchCmd(msg.ch)
	case fetchEvent:
		if msg.done {
			m.err = msg.err
			if msg.err == nil {
				m.done = true
				return m, nil
			}
			return m, tea.Quit
		}
		if msg.backedUp {
			m.backedUp = true
			return m, listenFetchCmd(m.ch)
		}
		m.sources[msg.language] = msg.source
		return m, listenFetchCmd(m.ch)
	}
	return m, nil
}

func (m downloadingModel) View() string {
	list := strings.Join(m.languages, ", ")
	if m.err != nil {
		return fmt.Sprintf("\n %s Error: %v\n", errorStyle.Render("x"), m.err)
	}
	if m.done {
		msg := fmt.Sprintf("\n %s Successfully created gitignore for %s\n", successStyle.Render("OK"), list)
		if m.backedUp {
			msg += fmt.Sprintf("\n Previous .gitignore saved to %s\n", dimStyle.Render(backupPath))
		}
		return msg + "\n Press any key to exit\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n %s Downloading gitignore for %s\n", m.spinner.View(), list)
	for _, language := range m.languages {
		if source, ok := m.sources[language]; ok {
			label := "downloaded"
			if source == gitignore.SourceCache {
				label = "from cache"
			}
			fmt.Fprintf(&b, "   %s %s\n", language, dimStyle.Render(label))
		}
	}
	return b.String()
}

func runPicker() error {
	program := tea.NewProgram(picker.New("Select environment", envs.List), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("could not run program: %w", err)
	}

	choices := finalModel.(picker.Model).Choices()
	if len(choices) == 0 {
		return nil
	}

	downloadProgram := tea.NewProgram(newDownloadingModel(choices))
	downloadFinal, err := downloadProgram.Run()
	if err != nil {
		return fmt.Errorf("could not run download program: %w", err)
	}

	if dm, ok := downloadFinal.(downloadingModel); ok && dm.err != nil {
		return dm.err
	}
	return nil
}

// runPickerFunc and updateListFunc are seams for runRoot, so tests can run
// the CLI end-to-end without touching the TUI or the network.
var (
	runPickerFunc  = runPicker
	updateListFunc = envlist.Update
)

// flagBool reads a registered bool flag, defaulting to false on any error.
func flagBool(cmd *cobra.Command, name string) bool {
	v, err := cmd.Flags().GetBool(name)
	return err == nil && v
}

// runRoot is the root action: dispatch the "do X and exit" flags, or fall
// through to the interactive picker.
func runRoot(cmd *cobra.Command, _ []string) error {
	// --no-cache applies to whatever runs next, even when another flag
	// short-circuits the picker below.
	if flagBool(cmd, "no-cache") {
		gitignore.BypassCache = true
	}

	switch {
	case flagBool(cmd, "update-list"):
		return updateListFunc()
	case flagBool(cmd, "clear-cache"):
		dir, err := gitignore.ClearCache()
		if err != nil {
			return err
		}
		fmt.Printf("cleared template cache at %s\n", dir)
		return nil
	}
	return runPickerFunc()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ignit",
		Short:         "quickly generate .gitignore files",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs, // stray words error instead of falling through to the picker
		RunE:          runRoot,
	}

	// Register --version before cobra's own so it gets the -v shorthand;
	// cobra detects the existing flag and uses it for its version handling.
	root.Flags().BoolP("version", "v", false, "version for ignit")
	root.Flags().Bool("clear-cache", false, "delete the on-disk template cache and exit")
	root.Flags().Bool("no-cache", false, "bypass the template cache: always fetch from the network (still refreshes the cache)")
	root.Flags().Bool("update-list", false, "re-fetch the template list from gitignore.io and regenerate the embedded list")

	// Cobra adds its own `completion` subcommand (lazily, since it is the
	// only one); `--help`/`-h` come from the built-in help flag. Silencing
	// usage/errors keeps error output in main's control.
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		log.Fatalf("error: %v", err)
	}
}
