package db

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// These cover the parts of the explorer that decide what SQL gets built —
// the identifier allowlist and the primary-key WHERE clause. Everything an
// explorer request can influence either becomes a $n parameter or is checked
// against the live catalog first; these tests pin that down.

var testCols = []Column{
	{Name: "id", Type: "integer", PrimaryKey: true},
	{Name: "tenant", Type: "text", PrimaryKey: true},
	{Name: "email", Type: "text", Nullable: true},
}

func TestCheckColumnRejectsUnknown(t *testing.T) {
	tests := []struct {
		name    string
		column  string
		wantErr bool
	}{
		{"known", "email", false},
		{"unknown", "nope", true},
		{"injection attempt", `email" ; DROP TABLE users --`, true},
		{"empty", "", true},
		{"case mismatch", "Email", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ident, err := checkColumn(testCols, tt.column)
			if tt.wantErr {
				if !errors.Is(err, ErrNoSuchColumn) {
					t.Fatalf("err = %v, want ErrNoSuchColumn", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ident != `"email"` {
				t.Errorf("ident = %s, want %q", ident, `"email"`)
			}
		})
	}
}

func TestBuildWhereRequiresFullPrimaryKey(t *testing.T) {
	tests := []struct {
		name string
		cols []Column
		key  map[string]any
		want string // empty means "expect an error"
	}{
		{
			name: "complete composite key",
			cols: testCols,
			key:  map[string]any{"id": 1, "tenant": "acme"},
			want: `"id" = $1 AND "tenant" = $2`,
		},
		{
			// A partial key would match every row sharing that column — the
			// one bug in an editor like this that silently destroys data.
			name: "partial key",
			cols: testCols,
			key:  map[string]any{"id": 1},
		},
		{
			name: "extra non-key column",
			cols: testCols,
			key:  map[string]any{"id": 1, "tenant": "acme", "email": "a@b.c"},
		},
		{
			name: "empty key",
			cols: testCols,
			key:  map[string]any{},
		},
		{
			name: "wrong column names",
			cols: testCols,
			key:  map[string]any{"id": 1, "other": 2},
		},
		{
			name: "table without a primary key",
			cols: []Column{{Name: "a", Type: "text"}},
			key:  map[string]any{"a": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args []any
			got, err := buildWhere(tt.cols, tt.key, &args)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("expected an error, got clause %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("clause = %q, want %q", got, tt.want)
			}
			if len(args) != len(tt.key) {
				t.Errorf("args = %d, want %d", len(args), len(tt.key))
			}
			// The values must travel as parameters, never inlined.
			if strings.Contains(got, "acme") {
				t.Errorf("clause %q inlined a value", got)
			}
		})
	}
}

func TestBuildWhereNoPrimaryKey(t *testing.T) {
	var args []any
	_, err := buildWhere([]Column{{Name: "a"}}, map[string]any{"a": 1}, &args)
	if !errors.Is(err, ErrNoPrimaryKey) {
		t.Errorf("err = %v, want ErrNoPrimaryKey", err)
	}
}

func TestUnknownColumns(t *testing.T) {
	if err := unknownColumns(testCols, map[string]any{"email": "x", "id": 1}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := unknownColumns(testCols, map[string]any{"email": "x", "bogus": 1}); !errors.Is(err, ErrNoSuchColumn) {
		t.Errorf("err = %v, want ErrNoSuchColumn", err)
	}
}

func TestPrimaryKey(t *testing.T) {
	got := primaryKey(testCols)
	if len(got) != 2 || got[0] != "id" || got[1] != "tenant" {
		t.Errorf("primaryKey = %v, want [id tenant]", got)
	}
	if got := primaryKey([]Column{{Name: "a"}}); len(got) != 0 {
		t.Errorf("primaryKey = %v, want empty", got)
	}
}

func TestJSONSafe(t *testing.T) {
	when := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value any
		want  any
	}{
		{"nil", nil, nil},
		{"string", "hi", "hi"},
		{"int", int64(7), int64(7)},
		{"bool", true, true},
		{"time", when, "2026-07-28T12:00:00Z"},
		{"bytes", []byte{0xde, 0xad}, `\xdead`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonSafe(tt.value); got != tt.want {
				t.Errorf("jsonSafe(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
