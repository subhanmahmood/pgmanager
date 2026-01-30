package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"pgmanager/internal/api"
	"pgmanager/internal/config"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
	"pgmanager/internal/tui"
)

var (
	// Version is set at build time via ldflags
	Version = "dev"

	cfgFile string
	cfg     *config.Config
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "pgmanager",
		Short: "PostgreSQL Database Manager",
		Long:  "A tool for managing PostgreSQL databases with project-based organization",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Skip config loading for help commands
			if cmd.Name() == "help" || cmd.Name() == "version" || cmd.Name() == "init" {
				return nil
			}

			var err error
			if cfgFile != "" {
				cfg, err = config.Load(cfgFile)
			} else {
				// Auto-discover config file
				path, discoverErr := config.Discover()
				if discoverErr != nil {
					return discoverErr
				}
				cfg, err = config.Load(path)
			}
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: auto-discover pgmanager.yaml)")

	// Project commands
	projectCmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}

	projectCreateCmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new project",
		Args:  cobra.ExactArgs(1),
		RunE:  projectCreate,
	}

	projectListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all projects",
		RunE:  projectList,
	}

	projectDeleteCmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a project and all its databases",
		Args:  cobra.ExactArgs(1),
		RunE:  projectDelete,
	}

	projectCmd.AddCommand(projectCreateCmd, projectListCmd, projectDeleteCmd)

	// Database commands
	dbCmd := &cobra.Command{
		Use:   "db",
		Short: "Manage databases",
	}

	dbCreateCmd := &cobra.Command{
		Use:   "create <project> <env> [pr-number]",
		Short: "Create a database for a project",
		Long:  "Create a database. env can be: prod, dev, staging, or pr\nFor PR databases, provide the PR number as the third argument.",
		Args:  cobra.RangeArgs(2, 3),
		RunE:  dbCreate,
	}

	dbDeleteCmd := &cobra.Command{
		Use:   "delete <project> <env> [pr-number]",
		Short: "Delete a database",
		Args:  cobra.RangeArgs(2, 3),
		RunE:  dbDelete,
	}

	dbListCmd := &cobra.Command{
		Use:   "list [project]",
		Short: "List databases",
		Args:  cobra.MaximumNArgs(1),
		RunE:  dbList,
	}

	dbInfoCmd := &cobra.Command{
		Use:   "info <project> <env> [pr-number]",
		Short: "Show database connection information",
		Args:  cobra.RangeArgs(2, 3),
		RunE:  dbInfo,
	}

	dbCmd.AddCommand(dbCreateCmd, dbDeleteCmd, dbListCmd, dbInfoCmd)

	// Cleanup command
	var olderThan string
	cleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Clean up old PR databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cleanup(olderThan)
		},
	}
	cleanupCmd.Flags().StringVar(&olderThan, "older-than", "7d", "Delete PR databases older than this duration (e.g., 7d, 24h)")

	// Serve command
	var port int
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve(port)
		},
	}
	serveCmd.Flags().IntVarP(&port, "port", "p", 8080, "Port to listen on")

	// TUI command
	tuiCmd := &cobra.Command{
		Use:   "tui",
		Short: "Start the interactive terminal UI",
		RunE:  runTUI,
	}

	// Version command
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("pgmanager %s\n", Version)
		},
	}

	// Init command
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Create a pgmanager.yaml config file in the current directory",
		RunE:  runInit,
	}

	rootCmd.AddCommand(projectCmd, dbCmd, cleanupCmd, serveCmd, tuiCmd, versionCmd, initCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func getStore(ctx context.Context) (meta.Store, error) {
	store, err := meta.NewPostgresStore(ctx, cfg.Postgres.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}
	return store, nil
}

func getManager(ctx context.Context) (*project.Manager, meta.Store, error) {
	store, err := getStore(ctx)
	if err != nil {
		return nil, nil, err
	}

	mgr := project.NewManager(cfg, store)
	return mgr, store, nil
}

func projectCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := mgr.CreateProject(ctx, args[0]); err != nil {
		return err
	}

	fmt.Printf("Project '%s' created successfully\n", args[0])
	return nil
}

func projectList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	projects, err := mgr.ListProjects(ctx)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		fmt.Println("No projects found")
		return nil
	}

	fmt.Printf("%-20s %-20s\n", "NAME", "CREATED")
	fmt.Println(strings.Repeat("-", 42))
	for _, p := range projects {
		fmt.Printf("%-20s %-20s\n", p.Name, p.CreatedAt.Format("2006-01-02 15:04"))
	}

	return nil
}

func projectDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := mgr.DeleteProject(ctx, args[0]); err != nil {
		return err
	}

	fmt.Printf("Project '%s' deleted successfully\n", args[0])
	return nil
}

func dbCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	projectName := args[0]
	env := args[1]

	var prNumber *int
	if env == "pr" {
		if len(args) < 3 {
			return fmt.Errorf("PR number is required for PR databases")
		}
		num, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[2])
		}
		prNumber = &num
	}

	info, err := mgr.CreateDatabase(ctx, projectName, env, prNumber)
	if err != nil {
		return err
	}

	fmt.Printf("Database created successfully\n")
	fmt.Printf("  Database: %s\n", info.DatabaseName)
	fmt.Printf("  User:     %s\n", info.UserName)
	fmt.Printf("  Password: %s\n", info.Password)
	fmt.Printf("  Host:     %s\n", info.Host)
	fmt.Printf("  Port:     %d\n", info.Port)
	fmt.Printf("\nConnection string:\n  %s\n", info.ConnString)

	return nil
}

func dbDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	projectName := args[0]
	env := args[1]

	var prNumber *int
	if env == "pr" {
		if len(args) < 3 {
			return fmt.Errorf("PR number is required for PR databases")
		}
		num, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[2])
		}
		prNumber = &num
	}

	if err := mgr.DeleteDatabase(ctx, projectName, env, prNumber); err != nil {
		return err
	}

	fmt.Println("Database deleted successfully")
	return nil
}

func dbList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	var projectName string
	if len(args) > 0 {
		projectName = args[0]
	}

	databases, err := mgr.ListDatabases(ctx, projectName)
	if err != nil {
		return err
	}

	if len(databases) == 0 {
		fmt.Println("No databases found")
		return nil
	}

	fmt.Printf("%-15s %-10s %-25s %-20s\n", "PROJECT", "ENV", "DATABASE", "CREATED")
	fmt.Println(strings.Repeat("-", 72))
	for _, db := range databases {
		envStr := db.Env
		if db.PRNumber != nil {
			envStr = fmt.Sprintf("pr_%d", *db.PRNumber)
		}
		fmt.Printf("%-15s %-10s %-25s %-20s\n",
			db.Project, envStr, db.DatabaseName, db.CreatedAt.Format("2006-01-02 15:04"))
	}

	return nil
}

func dbInfo(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	projectName := args[0]
	env := args[1]

	var prNumber *int
	if env == "pr" {
		if len(args) < 3 {
			return fmt.Errorf("PR number is required for PR databases")
		}
		num, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[2])
		}
		prNumber = &num
	}

	info, err := mgr.GetDatabase(ctx, projectName, env, prNumber)
	if err != nil {
		return err
	}

	fmt.Printf("Database: %s\n", info.DatabaseName)
	fmt.Printf("User:     %s\n", info.UserName)
	fmt.Printf("Host:     %s\n", info.Host)
	fmt.Printf("Port:     %d\n", info.Port)
	fmt.Printf("Created:  %s\n", info.CreatedAt.Format("2006-01-02 15:04:05"))
	if info.ExpiresAt != nil {
		fmt.Printf("Expires:  %s\n", info.ExpiresAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Println("\nNote: Password and connection string are only shown when the database is created.")

	return nil
}

func cleanup(olderThan string) error {
	ctx := context.Background()
	mgr, store, err := getManager(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	duration, err := parseDuration(olderThan)
	if err != nil {
		return fmt.Errorf("invalid duration: %w", err)
	}

	deleted, err := mgr.Cleanup(ctx, duration)
	if err != nil {
		return err
	}

	if len(deleted) == 0 {
		fmt.Println("No databases to clean up")
	} else {
		fmt.Printf("Deleted %d database(s):\n", len(deleted))
		for _, name := range deleted {
			fmt.Printf("  - %s\n", name)
		}
	}

	return nil
}

func serve(port int) error {
	ctx := context.Background()
	store, err := getStore(ctx)
	if err != nil {
		return err
	}

	mgr := project.NewManager(cfg, store)
	server := api.NewServer(cfg, mgr, port)

	fmt.Printf("Starting API server on port %d\n", port)
	return server.Start()
}

func runTUI(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	store, err := getStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()

	mgr := project.NewManager(cfg, store)
	return tui.Run(mgr)
}

// parseDuration parses a duration string like "7d", "24h", "1w"
func parseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("duration too short")
	}

	unit := s[len(s)-1]
	value, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, err
	}

	switch unit {
	case 's':
		return time.Duration(value) * time.Second, nil
	case 'm':
		return time.Duration(value) * time.Minute, nil
	case 'h':
		return time.Duration(value) * time.Hour, nil
	case 'd':
		return time.Duration(value) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(value) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit: %c", unit)
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	configContent := `# pgmanager configuration
# Metadata is stored in the PostgreSQL server itself (in the pgmanager schema)

postgres:
  host: localhost
  port: 5432
  user: postgres
  password: ""
  database: postgres
  ssl_mode: disable  # disable, require, verify-ca, verify-full

# API server config (for 'pgmanager serve')
api:
  port: 8080
  token: ""           # Set for API authentication
  require_token: true

# Cleanup settings
cleanup:
  default_ttl: 168h   # 7 days for PR databases
`

	// Check if config already exists
	for _, name := range config.ConfigFileNames {
		if _, err := os.Stat(name); err == nil {
			return fmt.Errorf("config file already exists: %s", name)
		}
	}

	// Write config file
	filename := "pgmanager.yaml"
	if err := os.WriteFile(filename, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("Created %s\n", filename)
	fmt.Println("\nEdit the file to set your PostgreSQL connection details.")
	fmt.Println("Environment variables can override config values:")
	fmt.Println("  POSTGRES_HOST, POSTGRES_PORT, POSTGRES_USER, POSTGRES_PASSWORD")
	return nil
}
