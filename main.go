package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var gitignoreAPI = "https://www.gitignore.io/api"

var environmentsPath = "environments.go"

type item struct {
	name string
}

func (i item) Title() string       { return i.name }
func (i item) Description() string { return "" }
func (i item) FilterValue() string { return i.name }

type pickModel struct {
	list   list.Model
	choice string
}

func newPickModel(items []string) pickModel {
	entries := make([]list.Item, 0, len(items))
	for _, name := range items {
		entries = append(entries, item{name: name})
	}

	const defaultWidth = 40
	delegate := list.NewDefaultDelegate()
	l := list.New(entries, delegate, defaultWidth, 24)
	l.Title = "Select environment"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	return pickModel{list: l}
}

func (m pickModel) Init() tea.Cmd { return nil }

func (m pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if selected, ok := m.list.SelectedItem().(item); ok {
				m.choice = selected.name
			}
			return m, tea.Quit
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickModel) View() string {
	return "\n" + m.list.View()
}

type loadingModel struct {
	spinner spinner.Model
	message string
	err     error
}

func newLoadingModel(message string) loadingModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	return loadingModel{spinner: s, message: message}
}

func (m loadingModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m loadingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m loadingModel) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}
	return fmt.Sprintf("\n %s %s\n", m.spinner.View(), m.message)
}

var (
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

type writeResultMsg struct {
	err error
}

func writeFileCmd(language string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Get(fmt.Sprintf("%s/%s", gitignoreAPI, language))
		if err != nil {
			return writeResultMsg{err: fmt.Errorf("failed to fetch gitignore for %s: %w", language, err)}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return writeResultMsg{err: fmt.Errorf("failed to fetch gitignore for %s: unexpected status %s", language, resp.Status)}
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return writeResultMsg{err: fmt.Errorf("failed to read gitignore response: %w", err)}
		}

		if err := os.WriteFile(".gitignore", body, 0644); err != nil {
			return writeResultMsg{err: fmt.Errorf("failed to write .gitignore: %w", err)}
		}

		return writeResultMsg{}
	}
}

type downloadingModel struct {
	spinner  spinner.Model
	language string
	result   writeResultMsg
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
		m.result = msg
		if msg.err == nil {
			m.done = true
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m downloadingModel) View() string {
	if m.result.err != nil {
		return fmt.Sprintf("\n %s Error: %v\n", errorStyle.Render("✗"), m.result.err)
	}
	if m.done {
		return fmt.Sprintf("\n %s Successfully created gitignore for %s\n\n Press any key to exit\n", successStyle.Render("✓"), m.language)
	}
	return fmt.Sprintf("\n %s Downloading gitignore for %s\n", m.spinner.View(), m.language)
}

// parseEnvironmentList splits the raw comma-separated API response into
// environment names, dropping empty entries.
func parseEnvironmentList(body string) []string {
	var items []string
	for _, raw := range strings.Split(body, ",") {
		if item := strings.TrimSpace(raw); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// renderEnvironmentList renders the environments slice as the content of
// a generated environments.go file.
func renderEnvironmentList(items []string) string {
	var sb strings.Builder
	sb.WriteString("package main\n\n")
	sb.WriteString("// Code generated by `ignit -update-list`. DO NOT EDIT.\n")
	sb.WriteString("// Source: https://www.toptal.com/developers/gitignore/api/list\n\n")
	sb.WriteString("// environments is a static snapshot of the template list from\n")
	sb.WriteString("// https://github.com/toptal/gitignore.io. The picker works fully\n")
	sb.WriteString("// offline; only writing the .gitignore hits the API.\n")
	sb.WriteString("var environments = []string{\n")
	for _, item := range items {
		sb.WriteString(fmt.Sprintf("\t%q,\n", item))
	}
	sb.WriteString("}\n")
	return sb.String()
}

// writeEnvironmentList writes the rendered list to path.
func writeEnvironmentList(path string, content string) error {
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// fetchEnvironmentList calls the gitignore.io list API and parses the result.
func fetchEnvironmentList() ([]string, error) {
	resp, err := http.Get(gitignoreAPI + "/list")
	if err != nil {
		return nil, fmt.Errorf("failed to list environments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to list environments: unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read environments response: %w", err)
	}

	items := parseEnvironmentList(string(body))
	if len(items) == 0 {
		return nil, fmt.Errorf("no environments returned by the API")
	}
	return items, nil
}

func updateList() error {
	items, err := fetchEnvironmentList()
	if err != nil {
		return err
	}

	if err := writeEnvironmentList(environmentsPath, renderEnvironmentList(items)); err != nil {
		return err
	}

	log.Printf("Updated %s with %d environments\n", environmentsPath, len(items))
	return nil
}

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-update-list":
			if err := updateList(); err != nil {
				log.Fatalf("error: %v", err)
			}
			return
		case "-v", "--version":
			fmt.Printf("ignit %s\n", version)
			return
		}
	}

	program := tea.NewProgram(newPickModel(environments))
	finalModel, err := program.Run()
	if err != nil {
		log.Fatalf("could not run program: %v", err)
	}

	m, ok := finalModel.(pickModel)
	if !ok {
		return
	}
	if m.choice == "" {
		fmt.Println("No environment selected")
		return
	}

	downloadProgram := tea.NewProgram(newDownloadingModel(m.choice))
	downloadFinal, err := downloadProgram.Run()
	if err != nil {
		log.Fatalf("could not run download program: %v", err)
	}

	if dm, ok := downloadFinal.(downloadingModel); ok {
		if dm.result.err != nil {
			log.Fatalf("error: %v", dm.result.err)
		}
	}
}
