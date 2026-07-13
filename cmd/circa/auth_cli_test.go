package main

import "testing"

func TestParseNameAndConfigOrderIndependent(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantName string
		wantPath string
	}{
		{"flag before name", []string{"-config", "c.yaml", "admin"}, "admin", "c.yaml"},
		{"flag after name", []string{"admin", "-config", "c.yaml"}, "admin", "c.yaml"},
		{"equals form", []string{"admin", "-config=c.yaml"}, "admin", "c.yaml"},
		{"no flag uses default", []string{"admin"}, "admin", "config.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, path, err := parseNameAndConfig(tc.args, "usage")
			if err != nil {
				t.Fatalf("parseNameAndConfig(%v): %v", tc.args, err)
			}
			if name != tc.wantName || path != tc.wantPath {
				t.Errorf("got (%q, %q), want (%q, %q)", name, path, tc.wantName, tc.wantPath)
			}
		})
	}
}

func TestParseNameAndConfigRejectsMissingOrExtraPositional(t *testing.T) {
	if _, _, err := parseNameAndConfig(nil, "usage"); err == nil {
		t.Error("expected error for no positional args")
	}
	if _, _, err := parseNameAndConfig([]string{"a", "b"}, "usage"); err == nil {
		t.Error("expected error for two positional args")
	}
	if _, _, err := parseNameAndConfig([]string{"-config"}, "usage"); err == nil {
		t.Error("expected error for -config with no value")
	}
}

func TestRunAuthHashPasswordRejectsArgs(t *testing.T) {
	if err := runAuthHashPassword([]string{"unexpected"}); err == nil {
		t.Error("expected error for unexpected positional arg")
	}
}

func TestRunAuthDispatchesHashPassword(t *testing.T) {
	err := runAuth([]string{"hash-password", "unexpected"})
	if err == nil {
		t.Fatal("expected error for unexpected positional arg via dispatch")
	}
}

func TestRunAuthRejectsUnknownSubcommand(t *testing.T) {
	if err := runAuth([]string{"bogus"}); err == nil {
		t.Error("expected error for unknown auth subcommand")
	}
}
