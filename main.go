// Command ignit is a CLI app that quickly generates .gitignore files.
//
// The root package is wiring only: flag handling, the picker/download
// program flow, and the gitignore download itself. The picker lives in
// internal/picker, the list generator in internal/envlist, and the
// embedded environment snapshot in internal/envs.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rubiin/ignit/internal/envlist"
	"github.com/rubiin/ignit/internal/envs"
	"github.com/rubiin/ignit/internal/picker"
)

// gitignoreAPI is the API used to download .gitignore templates.
var gitignoreAPI = "https://www.gitignore.io/api"

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// writeGitignore fetches the gitignore template for language and writes it
// to .gitignore in the current directory.
func writeGitignore(language string) error {
	resp, err := http.Get(fmt.Sprintf("%s/%s", gitignoreAPI, language))
	if err != nil {
		return fmt.Errorf("failed to fetch gitignore for %s: %w", language, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch gitignore for %s: unexpected status %s", language, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read gitignore response: %w", err)
	}

	if err := os.WriteFile(".gitignore", body, 0o644); err != nil {
		return fmt.Errorf("failed to write .gitignore: %w", err)
	}
	return nil
}

type writeResultMsg struct {
	err error
}

func writeFileCmd(language string) tea.Cmd {
	return func() tea.Msg {
		return writeResultMsg{err: writeGitignore(language)}
	}
}

// downloadingModel shows a spinner while the .gitignore downloads, then a
// success screen (any key exits) or the error.
type downloadingModel struct {
	spinner  spinner.Model
	language string
	err      error
	done     bool
}

func newDownloadingModel(language string) downloadingModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return downloadingModel{spinner: s, language: language}
}

func (m downloadingModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, writeFileCmd(m.language))
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
	case writeResultMsg:
		m.err = msg.err
		if msg.err == nil {
			m.done = true
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m downloadingModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n %s Error: %v\n", errorStyle.Render("x"), m.err)
	}
	if m.done {
		return fmt.Sprintf("\n %s Successfully created gitignore for %s\n\n Press any key to exit\n", successStyle.Render("OK"), m.language)
	}
	return fmt.Sprintf("\n %s Downloading gitignore for %s\n", m.spinner.View(), m.language)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-update-list":
			if err := envlist.Update(); err != nil {
				log.Fatalf("error: %v", err)
			}
			return
		case "-v", "--version":
			fmt.Printf("ignit %s\n", version)
			return
		}
	}

	program := tea.NewProgram(picker.New("Select environment", envs.List), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		log.Fatalf("could not run program: %v", err)
	}

	choice := finalModel.(picker.Model).Choice()
	if choice == "" {
		return
	}

	downloadProgram := tea.NewProgram(newDownloadingModel(choice))
	downloadFinal, err := downloadProgram.Run()
	if err != nil {
		log.Fatalf("could not run download program: %v", err)
	}

	if dm, ok := downloadFinal.(downloadingModel); ok && dm.err != nil {
		log.Fatalf("error: %v", dm.err)
	}
}
