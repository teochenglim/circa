package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func isBcryptHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

func TestMiddlewareNoUsersPassesThrough(t *testing.T) {
	called := false
	h := Middleware(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("expected handler to be called when no users configured")
	}
}

func TestMiddlewareRejectsMissingOrBadCreds(t *testing.T) {
	hash, err := HashPassword("s3cret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	users := map[string]string{"admin": hash}
	h := Middleware(users, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no creds: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad password: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "s3cret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("good creds: status = %d, want 200", rec.Code)
	}
}

func TestSetUserPreservesCommentsAndUpserts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "# a helpful comment\nserver:\n  listen_address: \":9090\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetUser(path, "admin", "s3cret"); err != nil {
		t.Fatalf("SetUser: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(data), "# a helpful comment") {
		t.Errorf("expected comment to survive edit, got:\n%s", data)
	}

	var out struct {
		Auth struct {
			Users map[string]string `yaml:"users"`
		} `yaml:"auth"`
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("re-parsing written file: %v", err)
	}
	if !isBcryptHash(out.Auth.Users["admin"]) {
		t.Errorf("auth.users.admin = %q, want a bcrypt hash", out.Auth.Users["admin"])
	}

	// Adding a second user must not clobber the first.
	if err := SetUser(path, "viewer", "otherpass"); err != nil {
		t.Fatalf("SetUser (viewer): %v", err)
	}
	data, _ = os.ReadFile(path)
	yaml.Unmarshal(data, &out)
	if !isBcryptHash(out.Auth.Users["admin"]) || !isBcryptHash(out.Auth.Users["viewer"]) {
		t.Errorf("expected both users present with bcrypt hashes, got %v", out.Auth.Users)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
