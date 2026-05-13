package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClientSendsToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Project{{Name: "demo"}})
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, "pgm_live_xyz")
	if _, err := c.ListProjects(context.Background()); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if gotAuth != "Bearer pgm_live_xyz" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
}

func TestHTTPClientAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "insufficient scope"})
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, "tok")
	err := c.DeleteProject(context.Background(), "x")
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusForbidden || !strings.Contains(apiErr.Message, "insufficient scope") {
		t.Errorf("got %+v", apiErr)
	}
}

func TestDBPath(t *testing.T) {
	pr := 42
	cases := []struct {
		project string
		env     string
		pr      *int
		want    string
	}{
		{"myapp", "dev", nil, "/projects/myapp/databases/dev"},
		{"myapp", "pr", &pr, "/projects/myapp/databases/pr_42"},
		{"my app", "dev", nil, "/projects/my%20app/databases/dev"},
	}
	for _, tc := range cases {
		got := dbPath(tc.project, tc.env, tc.pr)
		if got != tc.want {
			t.Errorf("dbPath(%s,%s,%v) = %q want %q", tc.project, tc.env, tc.pr, got, tc.want)
		}
	}
}
