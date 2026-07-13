// Package auth implements circa's basic-auth model per DESIGN/08 §8.2:
// optional, multi-user, bcrypt-hashed HTTP Basic Auth, stateless (no
// sessions, no CSRF surface), no-auth by default. It deliberately does not
// implement RBAC — every user in the list gets full access; see §8.2.1.
package auth

import (
	"fmt"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

// Middleware wraps h with HTTP Basic Auth checked against users (username ->
// bcrypt hash, config.yaml's auth.users). An empty/nil users map disables
// auth entirely and h is served unwrapped — this is the default, matching
// node_exporter/Prometheus.
func Middleware(users map[string]string, h http.Handler) http.Handler {
	if len(users) == 0 {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !authenticate(users, username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="circa"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// authenticate reports whether username/password match a user's stored
// bcrypt hash. A missing username still runs bcrypt against a dummy hash so
// the response time doesn't leak which usernames exist.
func authenticate(users map[string]string, username, password string) bool {
	hash, exists := users[username]
	if !exists {
		bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash keeps authenticate's timing constant when the username doesn't
// exist. Generated at startup (rather than a hardcoded literal) so no
// bcrypt-hash-shaped string sits in source — that trips secret-scanners
// (Semgrep's generic.secrets.security.detected-bcrypt-hash) even though
// it's never a real credential, just deliberately fixed input.
var dummyHash = generateDummyHash()

func generateDummyHash() string {
	hash, err := bcrypt.GenerateFromPassword([]byte("circa-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		panic("auth: failed to generate dummy hash: " + err.Error())
	}
	return string(hash)
}

// HashPassword bcrypt-hashes password at the default cost, for `circa auth
// add-user`/`reset-password` (§8.1.2) to write into config.yaml.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
