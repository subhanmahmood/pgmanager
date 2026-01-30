package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("46"))
)

type view int

const (
	viewProjects view = iota
	viewDatabases
	viewDatabaseInfo
)

type model struct {
	mgr            *project.Manager
	projects       []meta.Project
	databases      []project.DatabaseInfo
	selectedDB     *project.DatabaseInfo
	cursor         int
	currentView    view
	currentProject string
	err            error
	message        string
	width          int
	height         int
}

func initialModel(mgr *project.Manager) model {
	return model{
		mgr:         mgr,
		currentView: viewProjects,
	}
}

type projectsLoadedMsg []meta.Project
type databasesLoadedMsg []project.DatabaseInfo
type errMsg error
type successMsg string

func loadProjects(mgr *project.Manager) tea.Cmd {
	return func() tea.Msg {
		projects, err := mgr.ListProjects(context.Background())
		if err != nil {
			return errMsg(err)
		}
		return projectsLoadedMsg(projects)
	}
}

func loadDatabases(mgr *project.Manager, projectName string) tea.Cmd {
	return func() tea.Msg {
		databases, err := mgr.ListDatabases(context.Background(), projectName)
		if err != nil {
			return errMsg(err)
		}
		return databasesLoadedMsg(databases)
	}
}

func (m model) Init() tea.Cmd {
	return loadProjects(m.mgr)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyPress(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case projectsLoadedMsg:
		m.projects = msg
		m.cursor = 0
		m.err = nil
		return m, nil

	case databasesLoadedMsg:
		m.databases = msg
		m.cursor = 0
		m.err = nil
		return m, nil

	case errMsg:
		m.err = msg
		return m, nil

	case successMsg:
		m.message = string(msg)
		return m, nil
	}

	return m, nil
}

func (m model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.currentView == viewProjects {
			return m, tea.Quit
		}
		// Go back
		m.currentView = viewProjects
		m.currentProject = ""
		m.cursor = 0
		return m, loadProjects(m.mgr)

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		maxItems := m.getMaxItems()
		if m.cursor < maxItems-1 {
			m.cursor++
		}
		return m, nil

	case "enter":
		return m.handleEnter()

	case "esc", "b":
		if m.currentView == viewDatabaseInfo {
			m.currentView = viewDatabases
			m.selectedDB = nil
			return m, nil
		}
		if m.currentView == viewDatabases {
			m.currentView = viewProjects
			m.currentProject = ""
			m.cursor = 0
			return m, loadProjects(m.mgr)
		}
		return m, nil

	case "r":
		// Refresh
		if m.currentView == viewProjects {
			return m, loadProjects(m.mgr)
		}
		if m.currentView == viewDatabases {
			return m, loadDatabases(m.mgr, m.currentProject)
		}
		return m, nil
	}

	return m, nil
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.currentView {
	case viewProjects:
		if len(m.projects) > 0 && m.cursor < len(m.projects) {
			m.currentProject = m.projects[m.cursor].Name
			m.currentView = viewDatabases
			m.cursor = 0
			return m, loadDatabases(m.mgr, m.currentProject)
		}

	case viewDatabases:
		if len(m.databases) > 0 && m.cursor < len(m.databases) {
			m.selectedDB = &m.databases[m.cursor]
			m.currentView = viewDatabaseInfo
		}
	}

	return m, nil
}

func (m model) getMaxItems() int {
	switch m.currentView {
	case viewProjects:
		return len(m.projects)
	case viewDatabases:
		return len(m.databases)
	}
	return 0
}

func (m model) View() string {
	var s strings.Builder

	// Title
	title := "pgmanager"
	if m.currentProject != "" {
		title += " > " + m.currentProject
	}
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n")

	// Error message
	if m.err != nil {
		s.WriteString(errorStyle.Render("Error: " + m.err.Error()))
		s.WriteString("\n\n")
	}

	// Success message
	if m.message != "" {
		s.WriteString(successStyle.Render(m.message))
		s.WriteString("\n\n")
	}

	// Content based on view
	switch m.currentView {
	case viewProjects:
		s.WriteString(m.renderProjectsView())
	case viewDatabases:
		s.WriteString(m.renderDatabasesView())
	case viewDatabaseInfo:
		s.WriteString(m.renderDatabaseInfoView())
	}

	// Help
	s.WriteString("\n")
	s.WriteString(m.renderHelp())

	return s.String()
}

func (m model) renderProjectsView() string {
	var s strings.Builder

	if len(m.projects) == 0 {
		s.WriteString("No projects found. Create one with: pgmanager project create <name>\n")
		return s.String()
	}

	s.WriteString("Projects:\n\n")
	for i, p := range m.projects {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		line := fmt.Sprintf("%s%-20s %s", cursor, p.Name, p.CreatedAt.Format("2006-01-02"))
		s.WriteString(style.Render(line))
		s.WriteString("\n")
	}

	return s.String()
}

func (m model) renderDatabasesView() string {
	var s strings.Builder

	if len(m.databases) == 0 {
		s.WriteString("No databases found for this project.\n")
		s.WriteString("Create one with: pgmanager db create " + m.currentProject + " <env>\n")
		return s.String()
	}

	s.WriteString("Databases:\n\n")
	for i, db := range m.databases {
		cursor := "  "
		style := normalStyle
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		env := db.Env
		if db.PRNumber != nil {
			env = fmt.Sprintf("pr_%d", *db.PRNumber)
		}
		line := fmt.Sprintf("%s%-25s %-10s %s", cursor, db.DatabaseName, env, db.CreatedAt.Format("2006-01-02"))
		s.WriteString(style.Render(line))
		s.WriteString("\n")
	}

	return s.String()
}

func (m model) renderDatabaseInfoView() string {
	if m.selectedDB == nil {
		return "No database selected\n"
	}

	var s strings.Builder
	db := m.selectedDB

	s.WriteString("Database Information:\n\n")
	s.WriteString(fmt.Sprintf("  Database: %s\n", db.DatabaseName))
	s.WriteString(fmt.Sprintf("  User:     %s\n", db.UserName))
	s.WriteString(fmt.Sprintf("  Password: %s\n", db.Password))
	s.WriteString(fmt.Sprintf("  Host:     %s\n", db.Host))
	s.WriteString(fmt.Sprintf("  Port:     %d\n", db.Port))
	s.WriteString(fmt.Sprintf("  Created:  %s\n", db.CreatedAt.Format("2006-01-02 15:04:05")))
	if db.ExpiresAt != nil {
		s.WriteString(fmt.Sprintf("  Expires:  %s\n", db.ExpiresAt.Format("2006-01-02 15:04:05")))
	}
	s.WriteString("\n")
	s.WriteString("Connection String:\n")
	s.WriteString(fmt.Sprintf("  %s\n", db.ConnString))

	return s.String()
}

func (m model) renderHelp() string {
	var help string
	switch m.currentView {
	case viewProjects:
		help = "↑/k up • ↓/j down • enter select • r refresh • q quit"
	case viewDatabases:
		help = "↑/k up • ↓/j down • enter view • b/esc back • r refresh • q quit"
	case viewDatabaseInfo:
		help = "b/esc back • q quit"
	}
	return helpStyle.Render(help)
}

// Run starts the TUI application
func Run(mgr *project.Manager) error {
	p := tea.NewProgram(initialModel(mgr), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
