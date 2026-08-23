package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
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
