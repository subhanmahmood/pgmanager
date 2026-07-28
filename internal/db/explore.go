package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNoSuchTable is returned when a table is not present in the target
// database, or is in a schema the explorer refuses to touch.
var ErrNoSuchTable = errors.New("table not found")

// ErrNoPrimaryKey is returned for update/delete against a table that has no
// primary key — there is no safe way to address a single row without one.
var ErrNoPrimaryKey = errors.New("table has no primary key")

// ErrNoSuchColumn is returned when a request names a column the table does
// not have. Every identifier the explorer interpolates into SQL is checked
// against the live catalog first, so this is the guard that keeps identifier
// injection impossible.
var ErrNoSuchColumn = errors.New("unknown column")

// MaxRowLimit caps how many rows a single page request may return.
const MaxRowLimit = 200

// Table is one user table in the target database.
type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

// Column describes one column of a table.
type Column struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Nullable   bool    `json:"nullable"`
	Default    *string `json:"default,omitempty"`
	PrimaryKey bool    `json:"primary_key"`
}

// RowPage is a slice of a table's contents.
type RowPage struct {
	Columns []Column `json:"columns"`
	Rows    []Row    `json:"rows"`
	Total   int64    `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

// Row maps column name to a JSON-encodable value.
type Row map[string]any

// connectAs opens a connection to dbName as the database's own owner. The
// explorer deliberately never uses the configured admin credentials: whatever
// the caller can reach through the UI is exactly what that database's role can
// reach, so a bug here cannot escape into another project's data.
func (c *PostgresClient) connectAs(ctx context.Context, dbName, userName, password string) (*pgx.Conn, error) {
	sslMode := c.cfg.SSLMode
	if sslMode == "" {
		sslMode = "require"
	}
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.cfg.Host, c.cfg.Port, userName, password, dbName, sslMode)
	return pgx.Connect(ctx, connStr)
}

// ListTables returns the user tables visible to the database's own role.
func (c *PostgresClient) ListTables(ctx context.Context, dbName, userName, password string) ([]Table, error) {
	conn, err := c.connectAs(ctx, dbName, userName, password)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, `
		SELECT schemaname, tablename
		FROM pg_tables
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
		ORDER BY schemaname, tablename`)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer rows.Close()

	tables := []Table{}
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Schema, &t.Name); err != nil {
			return nil, fmt.Errorf("failed to scan table: %w", err)
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// describe loads the column list for a table, and doubles as the existence
// check: every other operation goes through it before building any SQL.
func describe(ctx context.Context, conn *pgx.Conn, schema, table string) ([]Column, error) {
	var exists bool
	err := conn.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_tables
			WHERE schemaname = $1 AND tablename = $2
			  AND schemaname NOT IN ('pg_catalog', 'information_schema'))`,
		schema, table).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to look up table: %w", err)
	}
	if !exists {
		return nil, ErrNoSuchTable
	}

	rows, err := conn.Query(ctx, `
		SELECT a.attname,
		       format_type(a.atttypid, a.atttypmod),
		       NOT a.attnotnull,
		       pg_get_expr(d.adbin, d.adrelid),
		       COALESCE(pk.is_pk, false)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		LEFT JOIN LATERAL (
			SELECT true AS is_pk
			FROM pg_index i
			WHERE i.indrelid = a.attrelid AND i.indisprimary
			  AND a.attnum = ANY(i.indkey)
		) pk ON true
		WHERE n.nspname = $1 AND c.relname = $2
		  AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("failed to describe table: %w", err)
	}
	defer rows.Close()

	cols := []Column{}
	for rows.Next() {
		var col Column
		if err := rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default, &col.PrimaryKey); err != nil {
			return nil, fmt.Errorf("failed to scan column: %w", err)
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// Describe returns the columns of a single table.
func (c *PostgresClient) Describe(ctx context.Context, dbName, userName, password, schema, table string) ([]Column, error) {
	conn, err := c.connectAs(ctx, dbName, userName, password)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)
	return describe(ctx, conn, schema, table)
}

func columnNames(cols []Column) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

func primaryKey(cols []Column) []string {
	var pk []string
	for _, c := range cols {
		if c.PrimaryKey {
			pk = append(pk, c.Name)
		}
	}
	return pk
}

// checkColumn verifies name is a real column of cols and returns its sanitized
// identifier. Callers must use the returned string — never the input — when
// building SQL.
func checkColumn(cols []Column, name string) (string, error) {
	for _, c := range cols {
		if c.Name == name {
			return pgx.Identifier{c.Name}.Sanitize(), nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrNoSuchColumn, name)
}

// SelectRows returns one page of a table, ordered by primary key when the
// table has one so paging is stable.
func (c *PostgresClient) SelectRows(ctx context.Context, dbName, userName, password, schema, table string, limit, offset int) (*RowPage, error) {
	if limit <= 0 || limit > MaxRowLimit {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	conn, err := c.connectAs(ctx, dbName, userName, password)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	cols, err := describe(ctx, conn, schema, table)
	if err != nil {
		return nil, err
	}
	qualified := pgx.Identifier{schema, table}.Sanitize()

	var total int64
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+qualified).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count rows: %w", err)
	}

	orderBy := ""
	if pk := primaryKey(cols); len(pk) > 0 {
		quoted := make([]string, len(pk))
		for i, name := range pk {
			// pk names come straight from the catalog, but sanitize anyway.
			quoted[i] = pgx.Identifier{name}.Sanitize()
		}
		orderBy = " ORDER BY " + strings.Join(quoted, ", ")
	}

	sql := fmt.Sprintf("SELECT * FROM %s%s LIMIT $1 OFFSET $2", qualified, orderBy)
	rows, err := conn.Query(ctx, sql, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to select rows: %w", err)
	}
	defer rows.Close()

	names := columnNames(cols)
	out := []Row{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("failed to read row: %w", err)
		}
		row := Row{}
		for i, v := range vals {
			if i < len(names) {
				row[names[i]] = jsonSafe(v)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read rows: %w", err)
	}

	return &RowPage{Columns: cols, Rows: out, Total: total, Limit: limit, Offset: offset}, nil
}

// jsonSafe converts a scanned Postgres value into something json.Marshal can
// render without losing the human-readable form. Types the driver hands back
// as Go structs (intervals, ranges, unknown OIDs) fall back to their string
// rendering rather than an object the UI could not display or send back.
func jsonSafe(v any) any {
	switch t := v.(type) {
	case nil, bool, string, float32, float64,
		int8, int16, int32, int64, uint8, uint16, uint32, uint64, int, uint:
		return t
	case []byte:
		return "\\x" + fmt.Sprintf("%x", t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case net.IP:
		return t.String()
	case fmt.Stringer:
		return t.String()
	}
	if b, err := json.Marshal(v); err == nil {
		return json.RawMessage(b)
	}
	return fmt.Sprintf("%v", v)
}

// InsertRow inserts a single row and returns it as stored.
func (c *PostgresClient) InsertRow(ctx context.Context, dbName, userName, password, schema, table string, values map[string]any) (Row, error) {
	conn, err := c.connectAs(ctx, dbName, userName, password)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	cols, err := describe(ctx, conn, schema, table)
	if err != nil {
		return nil, err
	}

	// Iterate the catalog order, not the map, so the generated SQL is stable.
	var idents, placeholders []string
	var args []any
	for _, col := range cols {
		v, ok := values[col.Name]
		if !ok {
			continue
		}
		idents = append(idents, pgx.Identifier{col.Name}.Sanitize())
		args = append(args, v)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	if err := unknownColumns(cols, values); err != nil {
		return nil, err
	}

	qualified := pgx.Identifier{schema, table}.Sanitize()
	var sql string
	if len(idents) == 0 {
		sql = fmt.Sprintf("INSERT INTO %s DEFAULT VALUES RETURNING *", qualified)
	} else {
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
			qualified, strings.Join(idents, ", "), strings.Join(placeholders, ", "))
	}

	return queryOneRow(ctx, conn, cols, sql, args)
}

// UpdateRow updates the row identified by key and returns it as stored.
func (c *PostgresClient) UpdateRow(ctx context.Context, dbName, userName, password, schema, table string, key, values map[string]any) (Row, error) {
	conn, err := c.connectAs(ctx, dbName, userName, password)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	cols, err := describe(ctx, conn, schema, table)
	if err != nil {
		return nil, err
	}
	if err := unknownColumns(cols, values); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errors.New("no columns to update")
	}

	var args []any
	var sets []string
	for _, col := range cols {
		v, ok := values[col.Name]
		if !ok {
			continue
		}
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", pgx.Identifier{col.Name}.Sanitize(), len(args)))
	}

	where, err := buildWhere(cols, key, &args)
	if err != nil {
		return nil, err
	}

	sql := fmt.Sprintf("UPDATE %s SET %s WHERE %s RETURNING *",
		pgx.Identifier{schema, table}.Sanitize(), strings.Join(sets, ", "), where)
	return queryOneRow(ctx, conn, cols, sql, args)
}

// DeleteRow deletes the row identified by key.
func (c *PostgresClient) DeleteRow(ctx context.Context, dbName, userName, password, schema, table string, key map[string]any) error {
	conn, err := c.connectAs(ctx, dbName, userName, password)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	cols, err := describe(ctx, conn, schema, table)
	if err != nil {
		return err
	}

	var args []any
	where, err := buildWhere(cols, key, &args)
	if err != nil {
		return err
	}

	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", pgx.Identifier{schema, table}.Sanitize(), where)
	tag, err := conn.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("failed to delete row: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("row not found")
	}
	return nil
}

// buildWhere renders a primary-key match. The key must name every primary-key
// column and nothing else, so a malformed request can never widen the match to
// more rows than the one the caller pointed at.
func buildWhere(cols []Column, key map[string]any, args *[]any) (string, error) {
	pk := primaryKey(cols)
	if len(pk) == 0 {
		return "", ErrNoPrimaryKey
	}
	if len(key) != len(pk) {
		return "", fmt.Errorf("key must specify exactly the primary key columns (%s)", strings.Join(pk, ", "))
	}

	var clauses []string
	for _, name := range pk {
		v, ok := key[name]
		if !ok {
			return "", fmt.Errorf("key is missing primary key column %q", name)
		}
		ident, err := checkColumn(cols, name)
		if err != nil {
			return "", err
		}
		*args = append(*args, v)
		clauses = append(clauses, fmt.Sprintf("%s = $%d", ident, len(*args)))
	}
	return strings.Join(clauses, " AND "), nil
}

func unknownColumns(cols []Column, values map[string]any) error {
	for name := range values {
		if _, err := checkColumn(cols, name); err != nil {
			return err
		}
	}
	return nil
}

func queryOneRow(ctx context.Context, conn *pgx.Conn, cols []Column, sql string, args []any) (Row, error) {
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := columnNames(cols)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("row not found")
	}
	vals, err := rows.Values()
	if err != nil {
		return nil, err
	}
	row := Row{}
	for i, v := range vals {
		if i < len(names) {
			row[names[i]] = jsonSafe(v)
		}
	}
	return row, rows.Err()
}
