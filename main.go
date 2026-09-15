// Command ignit is a CLI app that quickly generates .gitignore files.
//
// The root package is wiring only: flag handling, the picker/download
// program flow, and writing the merged .gitignore. The picker lives in
// internal/picker, the template fetching/merging in internal/gitignore, the
// list generator in internal/envlist, and the embedded environment snapshot
// in internal/envs.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v3"

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

// writeGitignore merges the template for each language client-side and
// writes the result to .gitignore in the current directory, streaming one
// event per completed fetch into progress. The channel must be buffered
// with room for every fetch so the callback never blocks.
func writeGitignore(languages []string, progress chan<- fetchEvent) error {
	content, err := gitignore.MergeWithProgress(languages, func(language string, source gitignore.Source) {
		progress <- fetchEvent{language: language, source: source}
	})
	if err != nil {
		return err
	}
	return gitignore.Write(".gitignore", content)
}

// fetchEvent is one progress update from the download goroutine: either a
// template fetch completing (with its origin) or the final result.
type fetchEvent struct {
	language string
	source   gitignore.Source
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
		return fmt.Sprintf("\n %s Successfully created gitignore for %s\n\n Press any key to exit\n", successStyle.Render("OK"), list)
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

// runRoot is the CLI's root action: dispatch the "do X and exit" flags, or
// fall through to the interactive picker.
func runRoot(_ context.Context, cmd *cli.Command) error {
	switch {
	case cmd.Bool("update-list"):
		return updateListFunc()
	case cmd.Bool("clear-cache"):
		dir, err := gitignore.ClearCache()
		if err != nil {
			return err
		}
		fmt.Printf("cleared template cache at %s\n", dir)
		return nil
	}
	return runPickerFunc()
}

func newCommand() *cli.Command {
	return &cli.Command{
		Name:                  "ignit",
		Usage:                 "quickly generate .gitignore files",
		Version:               version,
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "clear-cache",
				Usage: "delete the on-disk template cache and exit",
			},
			&cli.BoolFlag{
				Name:  "no-cache",
				Usage: "bypass the template cache: always fetch from the network (still refreshes the cache)",
				Action: func(_ context.Context, _ *cli.Command, _ bool) error {
					gitignore.BypassCache = true
					return nil
				},
			},
			&cli.BoolFlag{
				Name:  "update-list",
				Usage: "re-fetch the template list from gitignore.io and regenerate the embedded list",
			},
		},
		Action: runRoot,
	}
}

func main() {
	if err := newCommand().Run(context.Background(), os.Args); err != nil {
		log.Fatalf("error: %v", err)
	}
}
