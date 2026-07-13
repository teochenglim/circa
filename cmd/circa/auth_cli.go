package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/teochenglim/circa/internal/auth"
)

// runAuth dispatches `circa auth add-user <name>` / `circa auth
// reset-password <name>` / `circa auth hash-password` (DESIGN/08 §8.1.2,
// §8.2.2) — the CLI-driven, locally/SSH-run answer to password management;
// there's no self-service reset flow (see §8.2.2 for why).
func runAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: circa auth <add-user|reset-password|hash-password> ...")
	}
	switch args[0] {
	case "add-user":
		return runAuthAddUser(args[1:])
	case "reset-password":
		return runAuthResetPassword(args[1:])
	case "hash-password":
		return runAuthHashPassword(args[1:])
	default:
		return fmt.Errorf("unknown auth subcommand %q (want \"add-user\", \"reset-password\", or \"hash-password\")", args[0])
	}
}

func runAuthAddUser(args []string) error {
	username, configPath, err := parseNameAndConfig(args, "circa auth add-user <name> [-config config.yaml]")
	if err != nil {
		return err
	}

	users, err := readAuthUsers(configPath)
	if err != nil {
		return err
	}
	if _, exists := users[username]; exists {
		return fmt.Errorf("user %q already exists in %s — use \"circa auth reset-password %s\" to change their password", username, configPath, username)
	}

	return promptAndSetUser(configPath, username)
}

func runAuthResetPassword(args []string) error {
	username, configPath, err := parseNameAndConfig(args, "circa auth reset-password <name> [-config config.yaml]")
	if err != nil {
		return err
	}

	users, err := readAuthUsers(configPath)
	if err != nil {
		return err
	}
	if _, exists := users[username]; !exists {
		return fmt.Errorf("user %q not found in %s — use \"circa auth add-user %s\" to create them", username, configPath, username)
	}

	return promptAndSetUser(configPath, username)
}

// parseNameAndConfig extracts an optional -config/--config value (in either
// "-config x" or "-config=x" form, in any position) and the one remaining
// positional argument (the username) — order-independent, unlike
// flag.FlagSet, which stops parsing flags at the first positional argument.
func parseNameAndConfig(args []string, usage string) (name, configPath string, err error) {
	configPath = "config.yaml"
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("%s: missing value", a)
			}
			configPath = args[i+1]
			i++
		case strings.HasPrefix(a, "-config="):
			configPath = strings.TrimPrefix(a, "-config=")
		case strings.HasPrefix(a, "--config="):
			configPath = strings.TrimPrefix(a, "--config=")
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) != 1 || positional[0] == "" {
		return "", "", fmt.Errorf("usage: %s", usage)
	}
	return positional[0], configPath, nil
}

func promptAndSetUser(configPath, username string) error {
	password, err := readPasswordTwice("password for " + username + ": ")
	if err != nil {
		return err
	}
	if err := auth.SetUser(configPath, username, password); err != nil {
		return err
	}
	fmt.Printf("%s: password set for %q\n", configPath, username)
	return nil
}

// runAuthHashPassword implements `circa auth hash-password`: prompts for a
// password and prints only its bcrypt hash to stdout, touching no file —
// for pasting into config.yaml's auth.users by hand (docs, CI-generated
// configs, or editing several config files without the add-user/
// reset-password flow's file-writing side effect).
func runAuthHashPassword(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: circa auth hash-password")
	}
	password, err := readPasswordTwice("password: ")
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	fmt.Println(hash)
	return nil
}

// readPasswordTwice prompts for a password without echoing it, twice, and
// confirms both entries match.
func readPasswordTwice(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw1, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}

	fmt.Fprint(os.Stderr, "confirm password: ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}

	if string(pw1) != string(pw2) {
		return "", errors.New("passwords did not match")
	}
	return string(pw1), nil
}

// readAuthUsers reads just the auth.users map from an existing config file,
// deliberately not going through config.Load — a broken/incomplete config
// elsewhere shouldn't block adding a user, and config.Load's Validate would
// otherwise reject a fresh file that doesn't exist yet.
func readAuthUsers(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var partial struct {
		Auth struct {
			Users map[string]string `yaml:"users"`
		} `yaml:"auth"`
	}
	if err := yaml.Unmarshal(data, &partial); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return partial.Auth.Users, nil
}
