package db

import (
	"strings"
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	password := GeneratePassword()

	// Password should be 32 characters (16 bytes hex encoded)
	if len(password) != 32 {
		t.Errorf("password length = %d, want 32", len(password))
	}

	// Password should be hex (only 0-9 and a-f)
	for _, c := range password {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("password contains invalid character: %c", c)
		}
	}

	// Generate multiple passwords and ensure they're different
	password2 := GeneratePassword()
	if password == password2 {
		t.Error("generated passwords should be unique")
	}
}

func TestConnectionString(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		dbName   string
		userName string
		password string
		sslMode  string
		want     string
	}{
		{
			name:     "basic connection string with require ssl",
			host:     "localhost",
			port:     5432,
			dbName:   "myapp_prod",
			userName: "myapp_prod_user",
			password: "secret123",
			sslMode:  "require",
			want:     "postgresql://myapp_prod_user:secret123@localhost:5432/myapp_prod?sslmode=require",
		},
		{
			name:     "different port with disable ssl",
			host:     "db.example.com",
			port:     5433,
			dbName:   "testdb",
			userName: "testuser",
			password: "pass",
			sslMode:  "disable",
			want:     "postgresql://testuser:pass@db.example.com:5433/testdb?sslmode=disable",
		},
		{
			name:     "empty sslmode defaults to require",
			host:     "localhost",
			port:     5432,
			dbName:   "testdb",
			userName: "testuser",
			password: "pass",
			sslMode:  "",
			want:     "postgresql://testuser:pass@localhost:5432/testdb?sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConnectionString(tt.host, tt.port, tt.dbName, tt.userName, tt.password, tt.sslMode)
			if got != tt.want {
				t.Errorf("ConnectionString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuoteLiteral(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple string", "hello", "'hello'"},
		{"string with quote", "it's", "'it''s'"},
		{"string with multiple quotes", "it's a 'test'", "'it''s a ''test'''"},
		{"empty string", "", "''"},
		{"string with numbers", "pass123", "'pass123'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quoteLiteral(tt.input)
			if got != tt.want {
				t.Errorf("quoteLiteral(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestConnectionStringSpecialChars(t *testing.T) {
	// Test that special characters in password don't break the URL
	connStr := ConnectionString("localhost", 5432, "testdb", "user", "pass@word#123", "require")

	if !strings.Contains(connStr, "pass@word#123") {
		t.Error("connection string should contain the password with special characters")
	}
}
