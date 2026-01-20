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
		name     string
		project  string
		env      string
		prNumber *int
		want     string
	}{
		{"prod database", "myapp", "prod", nil, "myapp_prod"},
		{"dev database", "myapp", "dev", nil, "myapp_dev"},
		{"staging database", "myapp", "staging", nil, "myapp_staging"},
		{"pr database", "myapp", "pr", intPtr(123), "myapp_pr_123"},
		{"pr database with different number", "myapp", "pr", intPtr(456), "myapp_pr_456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DatabaseName(tt.project, tt.env, tt.prNumber)
			if got != tt.want {
				t.Errorf("DatabaseName(%q, %q, %v) = %q, want %q", tt.project, tt.env, tt.prNumber, got, tt.want)
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
		name       string
		input      string
		wantEnv    string
		wantPR     *int
		wantErr    bool
	}{
		{"prod environment", "prod", "prod", nil, false},
		{"dev environment", "dev", "dev", nil, false},
		{"pr environment", "pr_123", "pr", intPtr(123), false},
		{"pr environment high number", "pr_9999", "pr", intPtr(9999), false},
		{"invalid pr format", "pr_abc", "", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnv, gotPR, err := ParseEnv(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEnv(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if gotEnv != tt.wantEnv {
				t.Errorf("ParseEnv(%q) env = %q, want %q", tt.input, gotEnv, tt.wantEnv)
			}
			if (gotPR == nil) != (tt.wantPR == nil) {
				t.Errorf("ParseEnv(%q) prNumber = %v, want %v", tt.input, gotPR, tt.wantPR)
			}
			if gotPR != nil && tt.wantPR != nil && *gotPR != *tt.wantPR {
				t.Errorf("ParseEnv(%q) prNumber = %d, want %d", tt.input, *gotPR, *tt.wantPR)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}
