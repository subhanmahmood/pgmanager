package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ConnParams describes the database to connect pg_dump/pg_restore to. It is
// built the same way internal/db/explore.go's connectAs builds a connection:
// the database's own role and password, never the configured admin
// credentials — a backup or restore can only reach what that database's own
// role can already reach.
type ConnParams struct {
	Host     string
	Port     int
	DBName   string
	User     string
	Password string
	SSLMode  string // empty defaults to "require", matching connectAs.
}

// Dumper runs pg_dump and pg_restore as external processes.
//
// The zero value is usable and shells out to "pg_dump" / "pg_restore" on
// PATH — that is what callers outside this package should use in
// production. NewDumper spells that out explicitly. Tests within this
// package construct a Dumper{run: fakeRun} literal instead, so the suite
// never needs a real Postgres client binary.
type Dumper struct {
	DumpPath    string // default "pg_dump"
	RestorePath string // default "pg_restore"

	// run is injectable so tests never need a real Postgres client binary.
	// It receives the full argv (excluding the binary name) and env for the
	// subprocess, plus stdin/stdout pipes; stderr is always captured
	// separately and folded into the returned error. A nil run falls back
	// to actually exec'ing the named binary.
	run func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error
}

// NewDumper returns a Dumper that shells out to the real pg_dump/pg_restore
// binaries on PATH. This is the constructor production callers (the backup
// manager, internal/project) should use; tests build a Dumper literal with a
// fake run function instead.
func NewDumper() *Dumper {
	return &Dumper{
		DumpPath:    "pg_dump",
		RestorePath: "pg_restore",
		run:         execRun,
	}
}

func (d *Dumper) dumpPath() string {
	if d.DumpPath != "" {
		return d.DumpPath
	}
	return "pg_dump"
}

func (d *Dumper) restorePath() string {
	if d.RestorePath != "" {
		return d.RestorePath
	}
	return "pg_restore"
}

func (d *Dumper) runner() func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
	if d.run != nil {
		return d.run
	}
	return execRun
}

// execRun is the real implementation of Dumper.run: it execs name with args
// and env, wiring stdin/stdout as given and capturing stderr into the
// returned error on failure.
func execRun(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// connEnv builds the subprocess environment for a pg_dump/pg_restore
// invocation. The password goes here, as PGPASSWORD, never in argv — argv
// is world-readable via /proc, unlike a child process's environment block
// once exec has replaced it. PGSSLMODE mirrors connectAs's own default of
// "require" when the caller leaves SSLMode empty.
func connEnv(c ConnParams) []string {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = "require"
	}
	env := append(os.Environ(), "PGPASSWORD="+c.Password)
	env = append(env, "PGSSLMODE="+sslMode)
	return env
}

// connArgs builds the shared -h/-p/-U/-d flags used by both Dump and
// Restore.
func connArgs(c ConnParams) []string {
	return []string{
		"-h", c.Host,
		"-p", strconv.Itoa(c.Port),
		"-U", c.User,
		"-d", c.DBName,
	}
}

// Dump streams a custom-format (-Fc) pg_dump of c's database to out.
func (d *Dumper) Dump(ctx context.Context, c ConnParams, out io.Writer) error {
	args := append(connArgs(c), "--format=custom", "--no-owner", "--no-privileges")
	return d.runner()(ctx, d.dumpPath(), args, connEnv(c), nil, out)
}

// Restore runs pg_restore against c's database, reading a dump produced by
// Dump from in.
func (d *Dumper) Restore(ctx context.Context, c ConnParams, in io.Reader) error {
	args := append(connArgs(c), "--no-owner", "--no-privileges")
	return d.runner()(ctx, d.restorePath(), args, connEnv(c), in, nil)
}

// ClientMajorVersion runs "pg_dump --version" and parses the major version
// out of output shaped like "pg_dump (PostgreSQL) 17.11".
func ClientMajorVersion(ctx context.Context, d *Dumper) (int, error) {
	var out bytes.Buffer
	if err := d.runner()(ctx, d.dumpPath(), []string{"--version"}, nil, nil, &out); err != nil {
		return 0, fmt.Errorf("pg_dump --version: %w", err)
	}
	return parsePgDumpVersion(out.String())
}

// parsePgDumpVersion extracts the major version number from pg_dump
// --version output such as "pg_dump (PostgreSQL) 17.11\n" -> 17.
func parsePgDumpVersion(s string) (int, error) {
	s = strings.TrimSpace(s)
	idx := strings.LastIndex(s, ")")
	if idx == -1 || idx+1 >= len(s) {
		return 0, fmt.Errorf("unexpected pg_dump --version output: %q", s)
	}
	fields := strings.Fields(s[idx+1:])
	if len(fields) == 0 {
		return 0, fmt.Errorf("unexpected pg_dump --version output: %q", s)
	}
	verField := fields[0]
	if dot := strings.Index(verField, "."); dot != -1 {
		verField = verField[:dot]
	}
	major, err := strconv.Atoi(verField)
	if err != nil {
		return 0, fmt.Errorf("unexpected pg_dump --version output: %q", s)
	}
	return major, nil
}

// CheckCompatible fails loudly when the installed pg_dump client is older
// than the Postgres server it will be dumping from — an older client can
// silently miss server-side object types added after it was built.
func CheckCompatible(clientMajor, serverMajor int) error {
	if clientMajor < serverMajor {
		return fmt.Errorf(
			"pg_dump client is PostgreSQL %d but the server is PostgreSQL %d; rebuild the image with postgresql%d-client (or newer)",
			clientMajor, serverMajor, serverMajor,
		)
	}
	return nil
}

// Probe verifies that pg_dump and pg_restore are on PATH and that the
// installed client is new enough for serverMajor. Callers run this once at
// startup (when backups are enabled) so an incompatible or missing client
// fails loudly instead of failing on the first backup.
func Probe(ctx context.Context, d *Dumper, serverMajor int) error {
	if _, err := exec.LookPath(d.dumpPath()); err != nil {
		return fmt.Errorf("pg_dump not found on PATH: %w", err)
	}
	if _, err := exec.LookPath(d.restorePath()); err != nil {
		return fmt.Errorf("pg_restore not found on PATH: %w", err)
	}
	clientMajor, err := ClientMajorVersion(ctx, d)
	if err != nil {
		return err
	}
	return CheckCompatible(clientMajor, serverMajor)
}
