package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParsePgDumpVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{"pg_dump (PostgreSQL) 17.11\n", 17, false},
		{"pg_dump (PostgreSQL) 16.4", 16, false},
		{"pg_dump (PostgreSQL) 18.0", 18, false},
		{"  pg_dump (PostgreSQL) 15.2  \n", 15, false},
		{"not a version string", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := parsePgDumpVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePgDumpVersion(%q) = %d, nil; want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePgDumpVersion(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parsePgDumpVersion(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestClientMajorVersion(t *testing.T) {
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		if name != "pg_dump" {
			t.Errorf("name = %q, want pg_dump", name)
		}
		if len(args) != 1 || args[0] != "--version" {
			t.Errorf("args = %v, want [--version]", args)
		}
		_, err := io.WriteString(stdout, "pg_dump (PostgreSQL) 17.11\n")
		return err
	}}
	got, err := ClientMajorVersion(context.Background(), d)
	if err != nil {
		t.Fatalf("ClientMajorVersion: %v", err)
	}
	if got != 17 {
		t.Fatalf("ClientMajorVersion = %d, want 17", got)
	}
}

func TestClientMajorVersionPropagatesRunError(t *testing.T) {
	wantErr := errors.New("no such binary")
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		return wantErr
	}}
	if _, err := ClientMajorVersion(context.Background(), d); !errors.Is(err, wantErr) {
		t.Fatalf("ClientMajorVersion() = %v, want wraps %v", err, wantErr)
	}
}

func TestCheckCompatible(t *testing.T) {
	cases := []struct {
		client, server int
		wantErr        bool
	}{
		{16, 17, true},
		{17, 17, false},
		{18, 17, false},
	}
	for _, c := range cases {
		err := CheckCompatible(c.client, c.server)
		if c.wantErr && err == nil {
			t.Errorf("CheckCompatible(%d, %d) = nil, want error", c.client, c.server)
		}
		if !c.wantErr && err != nil {
			t.Errorf("CheckCompatible(%d, %d) = %v, want nil", c.client, c.server, err)
		}
	}
}

func TestDumpPassesPasswordInEnvNotArgs(t *testing.T) {
	var gotArgs, gotEnv []string
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		gotArgs = args
		gotEnv = env
		_, err := io.WriteString(stdout, "dump-bytes")
		return err
	}}

	c := ConnParams{
		Host:     "db.internal",
		Port:     5432,
		DBName:   "myapp_dev",
		User:     "myapp_dev_role",
		Password: "s3cr3t-password",
		SSLMode:  "require",
	}

	var out bytes.Buffer
	if err := d.Dump(context.Background(), c, &out); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if out.String() != "dump-bytes" {
		t.Fatalf("Dump did not forward stdout: got %q", out.String())
	}

	for _, a := range gotArgs {
		if strings.Contains(a, c.Password) {
			t.Fatalf("password leaked into argv: %v", gotArgs)
		}
	}

	found := false
	for _, e := range gotEnv {
		if e == "PGPASSWORD="+c.Password {
			found = true
		}
	}
	if !found {
		t.Fatalf("PGPASSWORD not present in env: %v", gotEnv)
	}

	wantArgs := []string{
		"-h", "db.internal", "-p", "5432", "-U", "myapp_dev_role", "-d", "myapp_dev",
		"--format=custom", "--no-owner", "--no-privileges",
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %q, want %q (full: %v)", i, gotArgs[i], wantArgs[i], gotArgs)
		}
	}
}

func TestRestorePassesPasswordInEnvNotArgs(t *testing.T) {
	var gotArgs, gotEnv []string
	var gotStdin []byte
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		gotArgs = args
		gotEnv = env
		var err error
		gotStdin, err = io.ReadAll(stdin)
		return err
	}}

	c := ConnParams{
		Host:     "db.internal",
		Port:     5432,
		DBName:   "myapp_dev_restore_1700000000",
		User:     "myapp_dev_restore_role",
		Password: "another-secret",
		// SSLMode left empty on purpose: connEnv should default it.
	}

	in := strings.NewReader("dump-payload")
	if err := d.Restore(context.Background(), c, in); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if string(gotStdin) != "dump-payload" {
		t.Fatalf("Restore did not forward stdin: got %q", gotStdin)
	}

	for _, a := range gotArgs {
		if strings.Contains(a, c.Password) {
			t.Fatalf("password leaked into argv: %v", gotArgs)
		}
	}

	wantArgs := []string{
		"-h", "db.internal", "-p", "5432", "-U", "myapp_dev_restore_role", "-d", "myapp_dev_restore_1700000000",
		"--no-owner", "--no-privileges",
	}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %q, want %q (full: %v)", i, gotArgs[i], wantArgs[i], gotArgs)
		}
	}

	foundPw, foundSSL := false, false
	for _, e := range gotEnv {
		if e == "PGPASSWORD="+c.Password {
			foundPw = true
		}
		if e == "PGSSLMODE=require" {
			foundSSL = true
		}
	}
	if !foundPw {
		t.Fatalf("PGPASSWORD not present in env: %v", gotEnv)
	}
	if !foundSSL {
		t.Fatalf("PGSSLMODE=require (default) not present in env: %v", gotEnv)
	}
}

func TestDumpPropagatesRunError(t *testing.T) {
	wantErr := errors.New("pg_dump exploded")
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		return wantErr
	}}
	if err := d.Dump(context.Background(), ConnParams{}, &bytes.Buffer{}); !errors.Is(err, wantErr) {
		t.Fatalf("Dump() = %v, want %v", err, wantErr)
	}
}

func TestRestorePropagatesRunError(t *testing.T) {
	wantErr := errors.New("pg_restore exploded")
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		return wantErr
	}}
	if err := d.Restore(context.Background(), ConnParams{}, strings.NewReader("x")); !errors.Is(err, wantErr) {
		t.Fatalf("Restore() = %v, want %v", err, wantErr)
	}
}

func TestNewDumperDefaults(t *testing.T) {
	d := NewDumper()
	if d.DumpPath != "pg_dump" {
		t.Errorf("DumpPath = %q, want pg_dump", d.DumpPath)
	}
	if d.RestorePath != "pg_restore" {
		t.Errorf("RestorePath = %q, want pg_restore", d.RestorePath)
	}
	if d.run == nil {
		t.Errorf("run is nil, want execRun (NewDumper must be usable to shell out for real)")
	}
}

func TestZeroValueDumperDefaultsPaths(t *testing.T) {
	d := &Dumper{}
	if got := d.dumpPath(); got != "pg_dump" {
		t.Errorf("dumpPath() = %q, want pg_dump", got)
	}
	if got := d.restorePath(); got != "pg_restore" {
		t.Errorf("restorePath() = %q, want pg_restore", got)
	}
}

// realTOC is verbatim "pg_restore -l" output from PostgreSQL 17 for a custom
// archive of a database holding two extensions (one of them untrusted), two
// tables and their constraints. Captured from a real pg_dump rather than
// written by hand, because the entry layout is what these functions parse.
const realTOC = `;
; Archive created at 2026-08-23 18:42:46 UTC
;     dbname: src
;     TOC Entries: 14
;     Compression: gzip
;     Dump Version: 1.16-0
;     Format: CUSTOM
;     Integer: 4 bytes
;     Offset: 8 bytes
;     Dumped from database version: 17.10
;     Dumped by pg_dump version: 17.10
;
;
; Selected TOC Entries:
;
3; 3079 16547 EXTENSION - hstore 
3573; 0 0 COMMENT - EXTENSION hstore 
2; 3079 16389 EXTENSION - pg_buffercache 
3574; 0 0 COMMENT - EXTENSION pg_buffercache 
221; 1259 16675 TABLE public h srcuser
220; 1259 16398 TABLE public t srcuser
3566; 0 16675 TABLE DATA public h srcuser
3565; 0 16398 TABLE DATA public t srcuser
3418; 2606 16681 CONSTRAINT public h h_pkey srcuser
3416; 2606 16404 CONSTRAINT public t t_pkey srcuser
`

func TestExtensionsInTOC(t *testing.T) {
	got := ExtensionsInTOC(realTOC)
	want := []string{"hstore", "pg_buffercache"}
	if len(got) != len(want) {
		t.Fatalf("ExtensionsInTOC() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExtensionsInTOC() = %v, want %v", got, want)
		}
	}

	// An archive without extensions must produce no names at all, so the
	// restore path stays exactly as it was.
	plain := ";\n; Selected TOC Entries:\n;\n219; 1259 16398 TABLE public t acme_dev_user\n"
	if got := ExtensionsInTOC(plain); len(got) != 0 {
		t.Errorf("ExtensionsInTOC(no extensions) = %v, want none", got)
	}
}

func TestFilterExtensionEntriesDropsOnlyOwnerOnlyEntries(t *testing.T) {
	filtered, dropped := FilterExtensionEntries(realTOC)

	if dropped != 4 {
		t.Errorf("dropped = %d, want 4 (two EXTENSION entries and their two COMMENTs)", dropped)
	}
	for _, gone := range []string{"EXTENSION - hstore", "EXTENSION - pg_buffercache", "COMMENT - EXTENSION"} {
		if strings.Contains(filtered, gone) {
			t.Errorf("filtered list still contains %q:\n%s", gone, filtered)
		}
	}
	// Everything a restore actually needs has to survive, untouched. A
	// filter that dropped a TABLE DATA entry would silently restore an
	// empty database.
	for _, keep := range []string{
		"221; 1259 16675 TABLE public h srcuser",
		"220; 1259 16398 TABLE public t srcuser",
		"3566; 0 16675 TABLE DATA public h srcuser",
		"3565; 0 16398 TABLE DATA public t srcuser",
		"3418; 2606 16681 CONSTRAINT public h h_pkey srcuser",
		"3416; 2606 16404 CONSTRAINT public t t_pkey srcuser",
	} {
		if !strings.Contains(filtered, keep) {
			t.Errorf("filtered list lost %q:\n%s", keep, filtered)
		}
	}
}

func TestFilterExtensionEntriesLeavesExtensionFreeArchivesAlone(t *testing.T) {
	plain := ";\n; Selected TOC Entries:\n;\n219; 1259 16398 TABLE public t acme_dev_user\n3463; 0 16398 TABLE DATA public t acme_dev_user\n"
	filtered, dropped := FilterExtensionEntries(plain)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
	if filtered != plain {
		t.Errorf("filtered = %q, want the input unchanged", filtered)
	}
}

func TestArchiveTOCRunsPgRestoreList(t *testing.T) {
	var gotStdin string
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		if name != "pg_restore" {
			t.Errorf("name = %q, want pg_restore", name)
		}
		if len(args) != 1 || args[0] != "-l" {
			t.Errorf("args = %v, want [-l]", args)
		}
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		gotStdin = string(b)
		_, err = io.WriteString(stdout, realTOC)
		return err
	}}

	toc, err := d.ArchiveTOC(context.Background(), strings.NewReader("archive-bytes"))
	if err != nil {
		t.Fatalf("ArchiveTOC: %v", err)
	}
	if gotStdin != "archive-bytes" {
		t.Errorf("stdin = %q, want the archive", gotStdin)
	}
	if toc != realTOC {
		t.Errorf("ArchiveTOC returned %q", toc)
	}
}

// RestoreSelected has to hand pg_restore a real file, since -L takes a path,
// and it must still keep the password out of argv.
func TestRestoreSelectedWritesListFileAndKeepsPasswordOutOfArgv(t *testing.T) {
	list, _ := FilterExtensionEntries(realTOC)

	var listPath, listBody string
	var gotArgs, gotEnv []string
	d := &Dumper{run: func(ctx context.Context, name string, args, env []string, stdin io.Reader, stdout io.Writer) error {
		gotArgs, gotEnv = args, env
		for i, a := range args {
			if a == "-L" && i+1 < len(args) {
				listPath = args[i+1]
				b, err := os.ReadFile(listPath)
				if err != nil {
					return err
				}
				listBody = string(b)
			}
		}
		return nil
	}}

	err := d.RestoreSelected(context.Background(), ConnParams{
		Host: "db.internal", Port: 5432, DBName: "acme_dev_restore_20260823T101500",
		User: "acme_dev_restore_20260823T101500_user", Password: "s3cret",
	}, strings.NewReader("archive-bytes"), list)
	if err != nil {
		t.Fatalf("RestoreSelected: %v", err)
	}

	if listPath == "" {
		t.Fatalf("args = %v, want a -L <path> pair", gotArgs)
	}
	if listBody != list {
		t.Errorf("list file body = %q, want the filtered list", listBody)
	}
	if _, err := os.Stat(listPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary list file %s survived the call (stat err = %v)", listPath, err)
	}
	for _, a := range gotArgs {
		if strings.Contains(a, "s3cret") {
			t.Fatalf("password leaked into argv: %v", gotArgs)
		}
	}
	if !slices.Contains(gotEnv, "PGPASSWORD=s3cret") {
		t.Errorf("env is missing PGPASSWORD")
	}
	for _, want := range []string{"--no-owner", "--no-privileges"} {
		if !slices.Contains(gotArgs, want) {
			t.Errorf("args = %v, want %s", gotArgs, want)
		}
	}
}
