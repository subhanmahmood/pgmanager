package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pgmanager/internal/client"
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
	c              client.Client
	projects       []client.Project
	databases      []client.Database
	selectedDB     *client.Database
	cursor         int
	currentView    view
	currentProject string
	err            error
	message        string
	width          int
	height         int
}

func initialModel(c client.Client) model {
	return model{c: c, currentView: viewProjects}
}

type projectsLoadedMsg []client.Project
type databasesLoadedMsg []client.Database
type credentialsLoadedMsg client.Database
type errMsg error
type successMsg string

func loadProjects(c client.Client) tea.Cmd {
	return func() tea.Msg {
		projects, err := c.ListProjects(context.Background())
		if err != nil {
			return errMsg(err)
		}
		return projectsLoadedMsg(projects)
	}
}

func loadDatabases(c client.Client, projectName string) tea.Cmd {
	return func() tea.Msg {
		databases, err := c.ListDatabases(context.Background(), projectName)
		if err != nil {
			return errMsg(err)
		}
		return databasesLoadedMsg(databases)
	}
}

func loadCredentials(c client.Client, projectName, env, key string) tea.Cmd {
	return func() tea.Msg {
		info, err := c.GetDatabaseCredentials(context.Background(), projectName, env, key)
		if err != nil {
			return errMsg(err)
		}
		return credentialsLoadedMsg(*info)
	}
}

func (m model) Init() tea.Cmd { return loadProjects(m.c) }

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

	case credentialsLoadedMsg:
		d := client.Database(msg)
		m.selectedDB = &d
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
		m.currentView = viewProjects
		m.currentProject = ""
		m.cursor = 0
		return m, loadProjects(m.c)

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

	case "p":
		// Fetch credentials for the currently selected database.
		if m.currentView == viewDatabaseInfo && m.selectedDB != nil {
			return m, loadCredentials(m.c, m.selectedDB.Project, m.selectedDB.Env, m.selectedDB.Key)
		}

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
			return m, loadProjects(m.c)
		}
		return m, nil

	case "r":
		if m.currentView == viewProjects {
			return m, loadProjects(m.c)
		}
		if m.currentView == viewDatabases {
			return m, loadDatabases(m.c, m.currentProject)
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
			return m, loadDatabases(m.c, m.currentProject)
		}
	case viewDatabases:
		if len(m.databases) > 0 && m.cursor < len(m.databases) {
			d := m.databases[m.cursor]
			m.selectedDB = &d
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

	title := "pgmanager"
	if m.currentProject != "" {
		title += " > " + m.currentProject
	}
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n")

	if m.err != nil {
		s.WriteString(errorStyle.Render("Error: " + m.err.Error()))
		s.WriteString("\n\n")
	}
	if m.message != "" {
		s.WriteString(successStyle.Render(m.message))
		s.WriteString("\n\n")
	}

	switch m.currentView {
	case viewProjects:
		s.WriteString(m.renderProjectsView())
	case viewDatabases:
		s.WriteString(m.renderDatabasesView())
	case viewDatabaseInfo:
		s.WriteString(m.renderDatabaseInfoView())
	}

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
		if db.Key != "" {
			env = db.Env + "_" + db.Key
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
	if db.Password != "" {
		s.WriteString(fmt.Sprintf("  Password: %s\n", db.Password))
	} else {
		s.WriteString("  Password: <hidden — press 'p' to reveal>\n")
	}
	s.WriteString(fmt.Sprintf("  Host:     %s\n", db.Host))
	s.WriteString(fmt.Sprintf("  Port:     %d\n", db.Port))
	s.WriteString(fmt.Sprintf("  Created:  %s\n", db.CreatedAt.Format("2006-01-02 15:04:05")))
	if db.ExpiresAt != nil {
		s.WriteString(fmt.Sprintf("  Expires:  %s\n", db.ExpiresAt.Format("2006-01-02 15:04:05")))
	}
	if db.ConnString != "" {
		s.WriteString("\nConnection String:\n")
		s.WriteString(fmt.Sprintf("  %s\n", db.ConnString))
	}
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
		help = "p reveal password • b/esc back • q quit"
	}
	return helpStyle.Render(help)
}

// Run starts the TUI application.
func Run(c client.Client) error {
	p := tea.NewProgram(initialModel(c), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
