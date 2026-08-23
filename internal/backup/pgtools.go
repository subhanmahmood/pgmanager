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

// RestoreSelected is Restore limited to the TOC entries listed in tocList,
// which is a "pg_restore -l" listing with unwanted entries removed (see
// FilterExtensionEntries). Everything absent from the list is skipped.
//
// -L takes a path, so the list is written to a temporary file for the life
// of the subprocess and removed afterwards. It holds nothing secret — object
// names and types, no credentials — but it is created 0600 anyway, since
// os.CreateTemp does that by default and there is no reason to widen it.
func (d *Dumper) RestoreSelected(ctx context.Context, c ConnParams, in io.Reader, tocList string) error {
	f, err := os.CreateTemp("", "pgmanager-restore-*.toc")
	if err != nil {
		return fmt.Errorf("failed to write the restore list: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := io.WriteString(f, tocList); err != nil {
		f.Close()
		return fmt.Errorf("failed to write the restore list: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to write the restore list: %w", err)
	}

	args := append(connArgs(c), "--no-owner", "--no-privileges", "-L", f.Name())
	return d.runner()(ctx, d.restorePath(), args, connEnv(c), in, nil)
}

// ArchiveTOC runs "pg_restore -l" over an archive read from in and returns
// the listing verbatim.
//
// Only the archive header and table of contents are consumed. In a custom
// (-Fc) archive both sit at the head, so a caller streaming an object out of
// a bucket pays for the first megabyte or so rather than the whole snapshot.
// Verified against PostgreSQL 17: "pg_restore -l" over a pipe carrying the
// first 1 MiB of a 20 MB archive, with the writer then stalling, still
// printed the complete TOC and exited immediately.
func (d *Dumper) ArchiveTOC(ctx context.Context, in io.Reader) (string, error) {
	var out bytes.Buffer
	if err := d.runner()(ctx, d.restorePath(), []string{"-l"}, nil, in, &out); err != nil {
		return "", fmt.Errorf("pg_restore -l: %w", err)
	}
	return out.String(), nil
}

// tocEntryFields splits one line of "pg_restore -l" output into its fields,
// or returns nil for anything that is not a selectable entry (blank lines
// and the ";"-prefixed header comments).
//
// An entry looks like:
//
//	2; 3079 16389 EXTENSION - pg_buffercache
//	3470; 0 0 COMMENT - EXTENSION pg_buffercache
//	219; 1259 16398 TABLE public t acme_dev_user
//
// so the returned fields are [tableoid, oid, type, schema, name...] — the
// type is always index 2.
func tocEntryFields(line string) []string {
	semi := strings.Index(line, ";")
	if semi <= 0 {
		return nil
	}
	if _, err := strconv.Atoi(strings.TrimSpace(line[:semi])); err != nil {
		return nil
	}
	fields := strings.Fields(line[semi+1:])
	if len(fields) < 3 {
		return nil
	}
	return fields
}

// isExtensionOwnerEntry reports whether a TOC entry can only be applied by
// the extension's owner — which, for every extension a superuser had to
// install, means only by a superuser. Those are the entries a restore
// running as the database's own ordinary role has to skip.
//
// Two kinds: the CREATE EXTENSION entry itself, and the COMMENT ON EXTENSION
// entry pg_dump emits beside it. Skipping only the first is not enough —
// verified against PostgreSQL 17, where pre-creating the extension makes
// "CREATE EXTENSION IF NOT EXISTS" a harmless no-op but the comment still
// fails with "must be owner of extension", which is enough to make
// pg_restore exit non-zero.
func isExtensionOwnerEntry(fields []string) bool {
	switch fields[2] {
	case "EXTENSION":
		return true
	case "COMMENT":
		return len(fields) >= 5 && fields[4] == "EXTENSION"
	}
	return false
}

// ExtensionsInTOC returns, in TOC order, the extensions an archive creates.
// Callers install these ahead of the restore, as a role allowed to (see
// internal/project's preCreateExtensions).
func ExtensionsInTOC(toc string) []string {
	var names []string
	for _, line := range strings.Split(toc, "\n") {
		fields := tocEntryFields(line)
		if fields == nil || fields[2] != "EXTENSION" || len(fields) < 5 {
			continue
		}
		names = append(names, fields[4])
	}
	return names
}

// FilterExtensionEntries returns a "pg_restore -L" list body holding every
// TOC entry except the ones only an extension owner could apply, plus the
// number of entries it dropped. Zero dropped means the archive has no
// extensions and the caller should restore it unmodified.
//
// Lines that are not entries (the header comments) are kept as they are:
// pg_restore ignores them, and keeping them makes the list readable if it
// ever ends up in a bug report.
func FilterExtensionEntries(toc string) (string, int) {
	lines := strings.Split(toc, "\n")
	kept := make([]string, 0, len(lines))
	dropped := 0
	for _, line := range lines {
		if fields := tocEntryFields(line); fields != nil && isExtensionOwnerEntry(fields) {
			dropped++
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), dropped
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
