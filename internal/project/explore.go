package project

import (
	"context"

	"pgmanager/internal/db"
)

// The explorer methods below all resolve the target through GetDatabase first,
// so a caller can only ever reach a database pgmanager created and recorded —
// never an arbitrary name — and the connection is made with that database's
// own credentials rather than the admin role.

func (m *Manager) explorerTarget(ctx context.Context, projectName, env, dbKey string) (*DatabaseInfo, error) {
	return m.GetDatabase(ctx, projectName, env, dbKey)
}

// ListTables returns the user tables in a managed database.
func (m *Manager) ListTables(ctx context.Context, projectName, env, dbKey string) ([]db.Table, error) {
	info, err := m.explorerTarget(ctx, projectName, env, dbKey)
	if err != nil {
		return nil, err
	}
	return m.pg.ListTables(ctx, info.DatabaseName, info.UserName, info.Password)
}

// DescribeTable returns the columns of one table in a managed database.
func (m *Manager) DescribeTable(ctx context.Context, projectName, env, dbKey string, schema, table string) ([]db.Column, error) {
	info, err := m.explorerTarget(ctx, projectName, env, dbKey)
	if err != nil {
		return nil, err
	}
	return m.pg.Describe(ctx, info.DatabaseName, info.UserName, info.Password, schema, table)
}

// SelectRows returns a page of rows from one table.
func (m *Manager) SelectRows(ctx context.Context, projectName, env, dbKey string, schema, table string, limit, offset int) (*db.RowPage, error) {
	info, err := m.explorerTarget(ctx, projectName, env, dbKey)
	if err != nil {
		return nil, err
	}
	return m.pg.SelectRows(ctx, info.DatabaseName, info.UserName, info.Password, schema, table, limit, offset)
}

// InsertRow inserts a row into one table.
func (m *Manager) InsertRow(ctx context.Context, projectName, env, dbKey string, schema, table string, values map[string]any) (db.Row, error) {
	info, err := m.explorerTarget(ctx, projectName, env, dbKey)
	if err != nil {
		return nil, err
	}
	return m.pg.InsertRow(ctx, info.DatabaseName, info.UserName, info.Password, schema, table, values)
}

// UpdateRow updates the row addressed by its primary key.
func (m *Manager) UpdateRow(ctx context.Context, projectName, env, dbKey string, schema, table string, key, values map[string]any) (db.Row, error) {
	info, err := m.explorerTarget(ctx, projectName, env, dbKey)
	if err != nil {
		return nil, err
	}
	return m.pg.UpdateRow(ctx, info.DatabaseName, info.UserName, info.Password, schema, table, key, values)
}

// DeleteRow deletes the row addressed by its primary key.
func (m *Manager) DeleteRow(ctx context.Context, projectName, env, dbKey string, schema, table string, key map[string]any) error {
	info, err := m.explorerTarget(ctx, projectName, env, dbKey)
	if err != nil {
		return err
	}
	return m.pg.DeleteRow(ctx, info.DatabaseName, info.UserName, info.Password, schema, table, key)
}
