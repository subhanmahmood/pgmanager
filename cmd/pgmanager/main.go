package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"pgmanager/internal/api"
	"pgmanager/internal/auth"
	"pgmanager/internal/client"
	"pgmanager/internal/config"
	cryptoutil "pgmanager/internal/crypto"
	"pgmanager/internal/meta"
	"pgmanager/internal/project"
	"pgmanager/internal/tui"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Global flags.
var (
	cfgFile       string
	profileFlag   string
	jsonOutput    bool
	clientCfg     *config.ClientConfig
	clientCfgPath string
)

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "pgmanager",
		Short:         "PostgreSQL Database Manager",
		Long:          "Project-based PostgreSQL database management with scoped tokens and an HTTP API.",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// `serve`, `version`, `init`, `keygen` do not use the client profile.
			switch cmd.Name() {
			case "serve", "version", "init", "keygen", "help":
				return nil
			}
			cfg, path, err := config.LoadClient()
			if err != nil {
				return err
			}
			clientCfg = cfg
			clientCfgPath = path
			return nil
		},
	}

	root.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "server config file path (for `serve`); auto-discovers pgmanager.yaml")
	root.PersistentFlags().StringVar(&profileFlag, "profile", "", "client profile to use (overrides $PGMANAGER_PROFILE and credentials.yaml current)")
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output JSON instead of human-readable tables")

	root.AddCommand(
		newProjectCmd(),
		newDBCmd(),
		newCleanupCmd(),
		newServeCmd(),
		newTUICmd(),
		newVersionCmd(),
		newInitCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newProfileCmd(),
		newAuthCmd(),
		newDoctorCmd(),
		newKeygenCmd(),
	)
	return root
}

// --- Client construction ----------------------------------------------------

// getClient resolves the active profile and returns a configured client.
// Callers must Close it.
func getClient(ctx context.Context) (client.Client, string, error) {
	name, profile, err := config.ResolveProfile(clientCfg, profileFlag)
	if err != nil {
		return nil, "", err
	}
	if profile.APIURL != "" {
		token := profile.Token
		if env := os.Getenv("PGMANAGER_API_TOKEN"); env != "" {
			token = env
		}
		if token == "" {
			return nil, name, fmt.Errorf("profile %q has no token; run: pgmanager login %s", name, profile.APIURL)
		}
		return client.NewHTTP(profile.APIURL, token), name, nil
	}
	if profile.Postgres != nil {
		// Build a temporary server config to feed OpenLocal.
		cfg := config.Default()
		cfg.Postgres = *profile.Postgres
		if profile.Crypto != nil {
			cfg.Crypto = *profile.Crypto
		}
		if key := os.Getenv("PGMANAGER_ENCRYPTION_KEY"); key != "" {
			cfg.Crypto.Key = key
		}
		c, err := client.OpenLocal(ctx, cfg)
		if err != nil {
			return nil, name, err
		}
		return c, name, nil
	}
	return nil, name, fmt.Errorf("profile %q is empty", name)
}

// --- project commands -------------------------------------------------------

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Manage projects"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "create <name>",
			Short: "Create a new project",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				if err := c.CreateProject(ctx, args[0]); err != nil {
					return err
				}
				return emit(map[string]string{"name": args[0], "status": "created"}, func() {
					fmt.Printf("Project %q created\n", args[0])
				})
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all projects",
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				projects, err := c.ListProjects(ctx)
				if err != nil {
					return err
				}
				return emit(projects, func() {
					if len(projects) == 0 {
						fmt.Println("No projects found")
						return
					}
					fmt.Printf("%-20s %-20s\n", "NAME", "CREATED")
					fmt.Println(strings.Repeat("-", 42))
					for _, p := range projects {
						fmt.Printf("%-20s %-20s\n", p.Name, p.CreatedAt.Format("2006-01-02 15:04"))
					}
				})
			},
		},
		&cobra.Command{
			Use:   "delete <name>",
			Short: "Delete a project and all its databases",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				if err := c.DeleteProject(ctx, args[0]); err != nil {
					return err
				}
				return emit(map[string]string{"name": args[0], "status": "deleted"}, func() {
					fmt.Printf("Project %q deleted\n", args[0])
				})
			},
		},
	)
	return cmd
}

// --- database commands ------------------------------------------------------

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "db", Short: "Manage databases"}

	var extensions []string
	createCmd := &cobra.Command{
		Use:   "create <project> <env> [pr-number]",
		Short: "Create a database",
		Long: "Create a database. env: prod, dev, staging, or pr (PR requires the PR number).\n\n" +
			"Repeat --extension/-x to install Postgres extensions in the new database\n" +
			"(e.g. --extension vector -x pg_trgm). Extensions usually require superuser,\n" +
			"so this runs with the server's admin credentials.",
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			project, env, prNumber, err := parseDBArgs(args)
			if err != nil {
				return err
			}
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			info, err := c.CreateDatabase(ctx, project, env, prNumber, extensions)
			if err != nil {
				return err
			}
			return emit(info, func() {
				printCredentials(info)
			})
		},
	}
	createCmd.Flags().StringSliceVarP(&extensions, "extension", "x", nil,
		"Postgres extension to install (repeatable; e.g. -x vector -x pg_trgm)")

	cmd.AddCommand(
		createCmd,
		&cobra.Command{
			Use:   "delete <project> <env> [pr-number]",
			Short: "Delete a database",
			Args:  cobra.RangeArgs(2, 3),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				project, env, prNumber, err := parseDBArgs(args)
				if err != nil {
					return err
				}
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				if err := c.DeleteDatabase(ctx, project, env, prNumber); err != nil {
					return err
				}
				return emit(map[string]string{"status": "deleted"}, func() {
					fmt.Println("Database deleted")
				})
			},
		},
		&cobra.Command{
			Use:   "list [project]",
			Short: "List databases (all, or for one project)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				var name string
				if len(args) > 0 {
					name = args[0]
				}
				dbs, err := c.ListDatabases(ctx, name)
				if err != nil {
					return err
				}
				return emit(dbs, func() {
					if len(dbs) == 0 {
						fmt.Println("No databases found")
						return
					}
					fmt.Printf("%-15s %-10s %-25s %-20s\n", "PROJECT", "ENV", "DATABASE", "CREATED")
					fmt.Println(strings.Repeat("-", 72))
					for _, db := range dbs {
						envStr := db.Env
						if db.PRNumber != nil {
							envStr = fmt.Sprintf("pr_%d", *db.PRNumber)
						}
						fmt.Printf("%-15s %-10s %-25s %-20s\n", db.Project, envStr, db.DatabaseName, db.CreatedAt.Format("2006-01-02 15:04"))
					}
				})
			},
		},
		&cobra.Command{
			Use:   "info <project> <env> [pr-number]",
			Short: "Show database connection information (no password)",
			Args:  cobra.RangeArgs(2, 3),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				project, env, prNumber, err := parseDBArgs(args)
				if err != nil {
					return err
				}
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				info, err := c.GetDatabase(ctx, project, env, prNumber)
				if err != nil {
					return err
				}
				return emit(info, func() {
					fmt.Printf("Database: %s\nUser:     %s\nHost:     %s\nPort:     %d\nCreated:  %s\n",
						info.DatabaseName, info.UserName, info.Host, info.Port, info.CreatedAt.Format("2006-01-02 15:04:05"))
					if info.ExpiresAt != nil {
						fmt.Printf("Expires:  %s\n", info.ExpiresAt.Format("2006-01-02 15:04:05"))
					}
					fmt.Println("\nFor password/connection string: pgmanager db credentials", info.Project, envOf(info))
				})
			},
		},
		&cobra.Command{
			Use:   "credentials <project> <env> [pr-number]",
			Short: "Fetch the connection string and password for an existing database",
			Args:  cobra.RangeArgs(2, 3),
			RunE: func(cmd *cobra.Command, args []string) error {
				ctx := cmd.Context()
				project, env, prNumber, err := parseDBArgs(args)
				if err != nil {
					return err
				}
				c, _, err := getClient(ctx)
				if err != nil {
					return err
				}
				defer c.Close()
				info, err := c.GetDatabaseCredentials(ctx, project, env, prNumber)
				if err != nil {
					return err
				}
				return emit(info, func() { printCredentials(info) })
			},
		},
	)
	return cmd
}

// --- cleanup, serve, tui, version -------------------------------------------

func newCleanupCmd() *cobra.Command {
	var olderThan string
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Delete expired and old PR databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			d, err := parseDurationStr(olderThan)
			if err != nil {
				return fmt.Errorf("invalid duration: %w", err)
			}
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			deleted, err := c.Cleanup(ctx, d)
			if err != nil {
				return err
			}
			return emit(map[string]interface{}{"deleted": deleted, "count": len(deleted)}, func() {
				if len(deleted) == 0 {
					fmt.Println("No databases to clean up")
					return
				}
				fmt.Printf("Deleted %d database(s):\n", len(deleted))
				for _, n := range deleted {
					fmt.Printf("  - %s\n", n)
				}
			})
		},
	}
	cmd.Flags().StringVar(&olderThan, "older-than", "7d", "delete PR databases older than this duration (e.g., 7d, 24h)")
	return cmd
}

func newServeCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadServerConfig()
			if err != nil {
				return err
			}
			if listen != "" {
				cfg.API.Listen = listen
			}

			ctx := cmd.Context()
			key, err := cfg.Crypto.EncryptionKey()
			if err != nil {
				return fmt.Errorf("encryption key: %w", err)
			}
			store, err := meta.NewPostgresStore(ctx, cfg.Postgres.ConnectionString(), key)
			if err != nil {
				return err
			}
			mgr := project.NewManager(cfg, store)
			server := api.NewServer(cfg, mgr, store, cfg.API.BindAddress())
			return server.Start()
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "address to bind (overrides config; defaults to 127.0.0.1:8080)")
	return cmd
}

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Start the interactive terminal UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			return tui.Run(c)
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("pgmanager %s\n", Version)
		},
	}
}

// --- init, login, logout, profile -------------------------------------------

func newInitCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize pgmanager for this machine",
		Long:  "Run with --mode=client to set up a CLI to talk to a remote pgmanager.\nRun with --mode=server to scaffold pgmanager.yaml for running `serve`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := strings.ToLower(mode)
			if m == "" {
				m = promptMode()
			}
			switch m {
			case "client", "c":
				return initClient()
			case "server", "s":
				return initServer()
			default:
				return fmt.Errorf("unknown --mode %q (want client or server)", mode)
			}
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "client | server")
	return cmd
}

func newLoginCmd() *cobra.Command {
	var profileName string
	cmd := &cobra.Command{
		Use:   "login <api-url>",
		Short: "Save an API token for a remote pgmanager",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			apiURL := strings.TrimRight(args[0], "/")
			cfg := clientCfg
			if cfg == nil {
				cfg = &config.ClientConfig{Profiles: map[string]*config.Profile{}}
			}
			name := profileName
			if name == "" {
				name = deriveProfileName(apiURL)
			}

			token, err := readToken(cmd)
			if err != nil {
				return err
			}

			// Validate by calling /auth/whoami.
			testClient := client.NewHTTP(apiURL, token)
			who, err := testClient.Whoami(cmd.Context())
			if err != nil {
				return fmt.Errorf("login failed: %w", err)
			}

			cfg.Profiles[name] = &config.Profile{APIURL: apiURL, Token: token}
			cfg.Current = name
			path, err := config.SaveClient(cfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved profile %q (token prefix %s, scopes %s) to %s\n",
				name, who.TokenPrefix, strings.Join(who.Scopes, ","), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&profileName, "name", "", "profile name (default: derived from URL host)")
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [profile]",
		Short: "Remove a saved profile",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientCfg == nil || len(clientCfg.Profiles) == 0 {
				return fmt.Errorf("no profiles configured")
			}
			name := clientCfg.Current
			if len(args) == 1 {
				name = args[0]
			}
			if _, ok := clientCfg.Profiles[name]; !ok {
				return fmt.Errorf("profile %q not found", name)
			}
			delete(clientCfg.Profiles, name)
			if clientCfg.Current == name {
				clientCfg.Current = ""
				for n := range clientCfg.Profiles {
					clientCfg.Current = n
					break
				}
			}
			path, err := config.SaveClient(clientCfg)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed profile %q from %s\n", name, path)
			return nil
		},
	}
}

func newProfileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Manage client profiles"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List configured profiles",
			RunE: func(cmd *cobra.Command, args []string) error {
				if clientCfg == nil || len(clientCfg.Profiles) == 0 {
					if jsonOutput {
						return emit([]string{}, nil)
					}
					fmt.Println("No profiles configured. Run: pgmanager login <api-url>")
					return nil
				}
				type row struct {
					Name    string `json:"name"`
					Mode    string `json:"mode"`
					APIURL  string `json:"api_url,omitempty"`
					Current bool   `json:"current"`
				}
				var rows []row
				for n, p := range clientCfg.Profiles {
					rows = append(rows, row{Name: n, Mode: p.Mode(), APIURL: p.APIURL, Current: n == clientCfg.Current})
				}
				return emit(rows, func() {
					fmt.Printf("%-20s %-8s %s\n", "NAME", "MODE", "URL/MODE")
					fmt.Println(strings.Repeat("-", 50))
					for _, r := range rows {
						marker := "  "
						if r.Current {
							marker = "* "
						}
						target := r.APIURL
						if target == "" {
							target = "(direct postgres)"
						}
						fmt.Printf("%s%-18s %-8s %s\n", marker, r.Name, r.Mode, target)
					}
				})
			},
		},
		&cobra.Command{
			Use:   "use <name>",
			Short: "Set the current profile",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if _, ok := clientCfg.Profiles[args[0]]; !ok {
					return fmt.Errorf("profile %q not found", args[0])
				}
				clientCfg.Current = args[0]
				path, err := config.SaveClient(clientCfg)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Current profile: %s (%s)\n", args[0], path)
				return nil
			},
		},
		&cobra.Command{
			Use:   "show",
			Short: "Show the current profile (token is not displayed)",
			RunE: func(cmd *cobra.Command, args []string) error {
				name, p, err := config.ResolveProfile(clientCfg, profileFlag)
				if err != nil {
					return err
				}
				view := map[string]interface{}{
					"name": name,
					"mode": p.Mode(),
				}
				if p.APIURL != "" {
					view["api_url"] = p.APIURL
					view["token_set"] = p.Token != ""
				}
				if p.Postgres != nil {
					view["postgres_host"] = p.Postgres.Host
					view["postgres_port"] = p.Postgres.Port
				}
				return emit(view, func() {
					fmt.Printf("name:       %s\nmode:       %s\n", name, p.Mode())
					if p.APIURL != "" {
						fmt.Printf("api_url:    %s\ntoken_set:  %v\n", p.APIURL, p.Token != "")
					}
					if p.Postgres != nil {
						fmt.Printf("postgres:   %s:%d\n", p.Postgres.Host, p.Postgres.Port)
					}
				})
			},
		},
	)
	return cmd
}

// --- auth & token CRUD ------------------------------------------------------

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage API tokens"}

	cmd.AddCommand(&cobra.Command{
		Use:   "whoami",
		Short: "Show the current token's prefix and scopes",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			who, err := c.Whoami(ctx)
			if err != nil {
				return err
			}
			return emit(who, func() {
				fmt.Printf("token: %s\nscopes: %s\n", who.TokenPrefix, strings.Join(who.Scopes, ", "))
			})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list-tokens",
		Short: "List API tokens",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			toks, err := c.ListTokens(ctx)
			if err != nil {
				return err
			}
			return emit(toks, func() {
				if len(toks) == 0 {
					fmt.Println("No tokens")
					return
				}
				fmt.Printf("%-16s %-25s %-30s %s\n", "PREFIX", "NAME", "SCOPES", "STATUS")
				fmt.Println(strings.Repeat("-", 95))
				for _, t := range toks {
					status := "active"
					if t.RevokedAt != nil {
						status = "revoked"
					} else if t.ExpiresAt != nil && time.Now().After(*t.ExpiresAt) {
						status = "expired"
					}
					fmt.Printf("%-16s %-25s %-30s %s\n", t.TokenPrefix, t.Name, strings.Join(t.Scopes, ","), status)
				}
			})
		},
	})

	createTokenCmd := &cobra.Command{
		Use:   "create-token",
		Short: "Create a new API token",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name, _ := cmd.Flags().GetString("name")
			scopes, _ := cmd.Flags().GetStringSlice("scope")
			expires, _ := cmd.Flags().GetString("expires")
			if name == "" || len(scopes) == 0 {
				return fmt.Errorf("--name and --scope are required")
			}
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			plain, info, err := c.CreateToken(ctx, name, scopes, expires)
			if err != nil {
				return err
			}
			return emit(map[string]interface{}{
				"token": plain,
				"info":  info,
			}, func() {
				fmt.Println("API TOKEN (save this — it will not be shown again):")
				fmt.Println("  " + plain)
				fmt.Printf("\nName:   %s\nPrefix: %s\nScopes: %s\n", info.Name, info.TokenPrefix, strings.Join(info.Scopes, ", "))
				if info.ExpiresAt != nil {
					fmt.Printf("Expires: %s\n", info.ExpiresAt.Format("2006-01-02 15:04"))
				}
			})
		},
	}
	createTokenCmd.Flags().String("name", "", "human-readable name (e.g., 'github-ci-myapp')")
	createTokenCmd.Flags().StringSlice("scope", nil, "scope (repeatable). Examples: admin | project:myapp | project:myapp:pr:* | project:myapp:env:dev")
	createTokenCmd.Flags().String("expires", "", "expiration duration (e.g., 90d, 24h); empty = no expiry")
	cmd.AddCommand(createTokenCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "revoke-token <prefix>",
		Short: "Revoke an API token by prefix",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := getClient(ctx)
			if err != nil {
				return err
			}
			defer c.Close()
			if err := c.RevokeToken(ctx, args[0]); err != nil {
				return err
			}
			return emit(map[string]string{"status": "revoked", "prefix": args[0]}, func() {
				fmt.Printf("Revoked token %s\n", args[0])
			})
		},
	})

	return cmd
}

// --- doctor & keygen --------------------------------------------------------

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the current client configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			report := map[string]interface{}{
				"credentials_path": clientCfgPath,
				"profile_count":    len(clientCfg.Profiles),
			}
			name, p, err := config.ResolveProfile(clientCfg, profileFlag)
			if err != nil {
				report["profile_error"] = err.Error()
				return emit(report, func() { printDoctor(report) })
			}
			report["profile"] = name
			report["mode"] = p.Mode()
			if p.APIURL != "" {
				report["api_url"] = p.APIURL
				report["token_set"] = p.Token != ""
			}
			c, _, err := getClient(ctx)
			if err != nil {
				report["client_error"] = err.Error()
				return emit(report, func() { printDoctor(report) })
			}
			defer c.Close()
			who, err := c.Whoami(ctx)
			if err != nil {
				report["whoami_error"] = err.Error()
			} else {
				report["whoami"] = who
			}
			return emit(report, func() { printDoctor(report) })
		},
	}
}

func newKeygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Generate a new at-rest encryption key (base64, 32 bytes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			k, err := cryptoutil.NewKey()
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"key": k})
			}
			fmt.Fprintln(cmd.OutOrStdout(), k)
			return nil
		},
	}
}

// --- helpers ----------------------------------------------------------------

func parseDBArgs(args []string) (project, env string, prNumber *int, err error) {
	project = args[0]
	env = args[1]
	if env == "pr" {
		if len(args) < 3 {
			return "", "", nil, fmt.Errorf("PR number is required for PR databases")
		}
		num, perr := strconv.Atoi(args[2])
		if perr != nil {
			return "", "", nil, fmt.Errorf("invalid PR number: %s", args[2])
		}
		prNumber = &num
	}
	return
}

func envOf(d *client.Database) string {
	if d.PRNumber != nil {
		return fmt.Sprintf("pr %d", *d.PRNumber)
	}
	return d.Env
}

func printCredentials(d *client.Database) {
	fmt.Printf("Database: %s\nUser:     %s\nPassword: %s\nHost:     %s\nPort:     %d\n",
		d.DatabaseName, d.UserName, d.Password, d.Host, d.Port)
	fmt.Printf("\nConnection string:\n  %s\n", d.ConnString)
	fmt.Printf("\nEnv export:\n  export DATABASE_URL=\"%s\"\n", d.ConnString)
}

func emit(data interface{}, human func()) error {
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(data)
	}
	if human != nil {
		human()
	}
	return nil
}

func parseDurationStr(s string) (time.Duration, error) {
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

// --- server-side config loader ----------------------------------------------

func loadServerConfig() (*config.Config, error) {
	var path string
	if cfgFile != "" {
		path = cfgFile
	} else {
		var err error
		path, err = config.Discover()
		if err != nil {
			return nil, err
		}
	}
	return config.Load(path)
}

// --- init helpers -----------------------------------------------------------

func promptMode() string {
	fmt.Print("Set up [c]lient (connect to remote pgmanager) or [s]erver (run pgmanager here)? ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(line))
}

func initClient() error {
	cfg, path, err := config.LoadClient()
	if err != nil {
		return err
	}
	if len(cfg.Profiles) > 0 {
		fmt.Printf("credentials.yaml already exists at %s with %d profile(s).\n", path, len(cfg.Profiles))
		fmt.Println("Run `pgmanager profile list` to see them, or `pgmanager login <url>` to add another.")
		return nil
	}
	if _, err := config.SaveClient(cfg); err != nil {
		return err
	}
	fmt.Printf("Created %s\n\nNext: pgmanager login https://your-pgmanager.example.com\n", path)
	return nil
}

func initServer() error {
	// Refuse to clobber any existing config file in cwd.
	for _, name := range config.ConfigFileNames {
		if _, err := os.Stat(name); err == nil {
			return fmt.Errorf("config file already exists: %s", name)
		}
	}
	key, err := cryptoutil.NewKey()
	if err != nil {
		return err
	}
	content := fmt.Sprintf(`# pgmanager server configuration.
# Metadata lives in the PostgreSQL server's `+"`pgmanager`"+` schema. Passwords
# stored there are AES-GCM encrypted with the key below.

postgres:
  host: localhost
  port: 5432
  user: postgres
  password: ""
  database: postgres
  ssl_mode: require   # use 'disable' only for local development

api:
  listen: 127.0.0.1:8080
  require_token: true
  # Front this with a reverse proxy (Caddy) for TLS.

cleanup:
  default_ttl: 168h

crypto:
  key: %q

data_dir: ./pgmanager-data
`, key)

	if err := os.WriteFile("pgmanager.yaml", []byte(content), 0o600); err != nil {
		return err
	}
	fmt.Println("Created pgmanager.yaml (mode 0600)")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Fill in postgres.password")
	fmt.Println("  2. Move 'crypto.key' to a secret manager and reference it via PGMANAGER_ENCRYPTION_KEY")
	fmt.Println("  3. Run: pgmanager serve")
	fmt.Println("  4. The first boot prints a bootstrap admin token to ./pgmanager-data/bootstrap-token.txt")
	return nil
}

func deriveProfileName(apiURL string) string {
	// Strip scheme.
	s := apiURL
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "default"
	}
	return s
}

func readToken(cmd *cobra.Command) (string, error) {
	// Token via env wins, then stdin (allows: `cat token.txt | pgmanager login URL`),
	// then interactive prompt.
	if env := os.Getenv("PGMANAGER_API_TOKEN"); env != "" {
		return env, nil
	}
	stat, _ := os.Stdin.Stat()
	if stat != nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		// Piped: read whole stdin, trim.
		b := bufio.NewReader(os.Stdin)
		line, _ := b.ReadString('\n')
		t := strings.TrimSpace(line)
		if t == "" {
			return "", fmt.Errorf("no token on stdin")
		}
		return t, nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Paste your token (starts with "+auth.TokenPrefix+"): ")
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func printDoctor(report map[string]interface{}) {
	fmt.Println("pgmanager doctor")
	fmt.Println(strings.Repeat("-", 40))
	for _, k := range []string{"credentials_path", "profile_count", "profile", "mode", "api_url", "token_set"} {
		if v, ok := report[k]; ok {
			fmt.Printf("%-18s %v\n", k+":", v)
		}
	}
	if v, ok := report["whoami"]; ok {
		w := v.(*client.Whoami)
		fmt.Printf("whoami:            %s (scopes: %s)\n", w.TokenPrefix, strings.Join(w.Scopes, ","))
	}
	for _, k := range []string{"profile_error", "client_error", "whoami_error"} {
		if v, ok := report[k]; ok {
			fmt.Printf("%-18s %v\n", k+":", v)
		}
	}
}
