package project

import (
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple name", "myapp", false},
		{"valid with numbers", "myapp123", false},
		{"valid with underscore", "my_app", false},
		{"valid long name", "my_super_cool_application", false},
		{"too short", "a", true},
		{"too long", "this_name_is_way_too_long_for_a_project_name", true},
		{"starts with number", "123app", true},
		{"contains hyphen", "my-app", true},
		{"contains uppercase", "MyApp", true},
		{"reserved name postgres", "postgres", true},
		{"reserved name admin", "admin", true},
		{"reserved name root", "root", true},
		{"reserved name template0", "template0", true},
		{"empty string", "", true},
		{"contains space", "my app", true},
		{"starts with underscore", "_myapp", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateExtensionName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"vector", "vector", false},
		{"pg_trgm", "pg_trgm", false},
		{"uuid-ossp (canonical hyphen)", "uuid-ossp", false},
		{"camelCase allowed", "CitusDB", false},
		{"empty", "", true},
		{"starts with digit", "1ext", true},
		{"starts with underscore", "_ext", true},
		{"starts with hyphen", "-ext", true},
		{"contains space", "bad ext", true},
		{"contains semicolon (injection)", "vector;DROP TABLE x", true},
		{"contains quote", "ext'or'1", true},
		{"too long (64 chars)", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExtensionName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateExtensionName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateEnv(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"prod environment", "prod", false},
		{"dev environment", "dev", false},
		{"staging environment", "staging", false},
		{"pr environment", "pr", false},
		{"invalid environment", "test", true},
		{"empty environment", "", true},
		{"uppercase", "PROD", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEnv(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEnv(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestDatabaseName(t *testing.T) {
	tests := []struct {
		name    string
		project string
		env     string
		key     string
		want    string
	}{
		{"prod database", "myapp", "prod", "", "myapp_prod"},
		{"dev database", "myapp", "dev", "", "myapp_dev"},
		{"staging database", "myapp", "staging", "", "myapp_staging"},
		{"pr database", "myapp", "pr", "123", "myapp_pr_123"},
		{"pr database with different number", "myapp", "pr", "456", "myapp_pr_456"},
		{"scratch database", "myapp", "scratch", "epic_231", "myapp_scratch_epic_231"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DatabaseName(tt.project, tt.env, tt.key)
			if got != tt.want {
				t.Errorf("DatabaseName(%q, %q, %q) = %q, want %q", tt.project, tt.env, tt.key, got, tt.want)
			}
		})
	}
}

func TestUserName(t *testing.T) {
	tests := []struct {
		name   string
		dbName string
		want   string
	}{
		{"prod user", "myapp_prod", "myapp_prod_user"},
		{"dev user", "myapp_dev", "myapp_dev_user"},
		{"pr user", "myapp_pr_123", "myapp_pr_123_user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UserName(tt.dbName)
			if got != tt.want {
				t.Errorf("UserName(%q) = %q, want %q", tt.dbName, got, tt.want)
			}
		})
	}
}

func TestParseEnv(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantEnv string
		wantKey string
		wantErr bool
	}{
		{"prod environment", "prod", "prod", "", false},
		{"dev environment", "dev", "dev", "", false},
		{"pr environment", "pr_123", "pr", "123", false},
		{"pr environment high number", "pr_9999", "pr", "9999", false},
		{"invalid pr format", "pr_abc", "", "", true},
		{"scratch environment", "scratch_epic_231", "scratch", "epic_231", false},
		{"scratch key keeps later underscores", "scratch_a_b_c", "scratch", "a_b_c", false},
		{"scratch key must start with a letter", "scratch_9lives", "", "", true},
		// An underscore in a non-keyed env is not a separator, so the whole
		// segment stays the env and ValidateEnv rejects it later.
		{"underscore in unkeyed env", "dev_extra", "dev_extra", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnv, gotKey, err := ParseEnv(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEnv(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if gotEnv != tt.wantEnv || gotKey != tt.wantKey {
				t.Errorf("ParseEnv(%q) = (%q, %q), want (%q, %q)", tt.input, gotEnv, gotKey, tt.wantEnv, tt.wantKey)
			}
		})
	}
}
