// Package config loads circa's single YAML config file.
//
// server, ingest.scrape, storage, push, and auth are read and acted on as of
// v0.3.0. alerting and backup are decoded into Config (so `circa config
// check` has something to validate) but not yet acted on — those land in
// v0.4.0/v0.5.0.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration so it can be parsed from a YAML string like
// "15s" or "2h" — or "7d"/"365d" (retention is naturally day/year-scale, and
// Go's own time.ParseDuration has no unit above "h").
type Duration time.Duration

func (d Duration) String() string {
	return time.Duration(d).String()
}

// MarshalYAML renders as the same string form Duration parses (e.g.
// "1h0m0s"), not the raw nanosecond count — matters for GET /status (§8.3),
// which marshals the effective Config back out as human-readable YAML.
func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

var dayOrYearUnit = regexp.MustCompile(`^(\d+)([dy])$`)

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}

	if m := dayOrYearUnit.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		hoursPerUnit := 24
		if m[2] == "y" {
			hoursPerUnit = 24 * 365
		}
		*d = Duration(time.Duration(n*hoursPerUnit) * time.Hour)
		return nil
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

type Config struct {
	Server   Server   `yaml:"server"`
	Features Features `yaml:"features"`
	Ingest   Ingest   `yaml:"ingest"`
	Storage  Storage  `yaml:"storage"`
	Alerting Alerting `yaml:"alerting"`
	Backup   Backup   `yaml:"backup"`
	Push     Push     `yaml:"push"`
	Auth     Auth     `yaml:"auth"`
}

type Server struct {
	ListenAddress string `yaml:"listen_address"`
}

// Features are the master on/off switches for every optional subsystem.
// None of them are acted on yet in v0.1.0 — ingest.scrape and storage are
// the only always-on pieces this milestone builds.
type Features struct {
	ML            bool `yaml:"ml"`
	Alerts        bool `yaml:"alerts"`
	Backup        bool `yaml:"backup"`
	InfluxReceive bool `yaml:"influx_receive"`
	PushReceive   bool `yaml:"push_receive"`
	PushSend      bool `yaml:"push_send"`
}

type Ingest struct {
	Scrape ScrapeConfig `yaml:"scrape"`
	Influx InfluxConfig `yaml:"influx"`
}

// InfluxConfig is decoded and validated as of v0.3.0 (path required whenever
// features.influx_receive is on) but the receiver itself isn't built yet —
// that's internal/ingest/influx, still planned.
type InfluxConfig struct {
	Path string `yaml:"path"`
}

type ScrapeConfig struct {
	Targets []ScrapeTarget `yaml:"targets"`
}

type ScrapeTarget struct {
	URL      string            `yaml:"url"`
	Interval Duration          `yaml:"interval"`
	Labels   map[string]string `yaml:"labels"`
}

type Storage struct {
	Path      string    `yaml:"path"`
	Retention Retention `yaml:"retention"`
}

type Retention struct {
	Raw    Duration `yaml:"raw"`
	Minute Duration `yaml:"minute"`
	Hour   Duration `yaml:"hour"`
}

// Alerting is decoded so `circa config check` has a schema to validate
// against, but the rule engine itself is v0.4.0 — nothing reads Rules or
// Notifiers yet. Kept loosely typed (yaml.Node) since that schema hasn't
// been designed yet; see DESIGN/06.
type Alerting struct {
	Rules     []yaml.Node `yaml:"rules"`
	Notifiers []yaml.Node `yaml:"notifiers"`
}

// Backup is decoded so `circa config check` can validate it (per DESIGN/08
// §8.1.2's "backup.mode is pull but no catalog URI set" example) ahead of
// the exporter itself, which is v0.5.0.
type Backup struct {
	Mode     string        `yaml:"mode"` // "push" or "pull"
	Schedule string        `yaml:"schedule"`
	Catalog  BackupCatalog `yaml:"catalog"`
}

type BackupCatalog struct {
	URI       string `yaml:"uri"`
	Warehouse string `yaml:"warehouse"`
}

// Push holds the two remote-write directions from DESIGN/04 §4.4, both
// feature-flagged and off by default.
type Push struct {
	Receive PushReceive `yaml:"receive"`
	Send    PushSend    `yaml:"send"`
}

type PushReceive struct {
	Path string `yaml:"path"`
}

type PushSend struct {
	URL      string   `yaml:"url"`
	Interval Duration `yaml:"interval"`
}

// Auth holds bcrypt-hashed basic-auth credentials, per DESIGN/08 §8.2. An
// empty Users map (the default) means no auth is required.
type Auth struct {
	Users map[string]string `yaml:"users"`
}

// DefaultPushReceivePath and DefaultPushSendInterval are applied when the
// corresponding feature is on but the field was left unset in the file.
const (
	DefaultPushReceivePath = "/api/v1/write"
	DefaultInfluxPath      = "/write"
)

// Default returns the config used when no file is given, or when a field is
// left unset in the file — a fresh install with an empty target list should
// still start up and serve an empty dashboard, per DESIGN/04 §4.2.
func Default() Config {
	return Config{
		Server: Server{ListenAddress: ":9100"},
		Storage: Storage{
			Path: "./data",
			Retention: Retention{
				Raw:    Duration(2 * time.Hour),
				Minute: Duration(7 * 24 * time.Hour),
				Hour:   Duration(365 * 24 * time.Hour),
			},
		},
		Ingest: Ingest{Influx: InfluxConfig{Path: DefaultInfluxPath}},
		Push: Push{
			Receive: PushReceive{Path: DefaultPushReceivePath},
			Send:    PushSend{Interval: Duration(30 * time.Second)},
		},
	}
}

// Load reads and parses the YAML config at path, then validates it (see
// Validate) — a bad config fails at startup rather than partway through
// wiring up subsystems. An empty path returns Default() unchanged — `circa`
// should run with no config file present.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config %s: %w", path, err)
	}
	cfg.applyDefaults()

	if errs := cfg.Validate(); len(errs) > 0 {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, errors.Join(errs...))
	}

	return cfg, nil
}

// applyDefaults fills in zero-value fields left unset in the file. Unlike
// Default(), this runs after unmarshalling so it only touches fields the
// file didn't set.
func (cfg *Config) applyDefaults() {
	if cfg.Server.ListenAddress == "" {
		cfg.Server.ListenAddress = Default().Server.ListenAddress
	}
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = Default().Storage.Path
	}
	if cfg.Storage.Retention.Raw == 0 {
		cfg.Storage.Retention.Raw = Default().Storage.Retention.Raw
	}
	if cfg.Ingest.Influx.Path == "" {
		cfg.Ingest.Influx.Path = DefaultInfluxPath
	}
	if cfg.Push.Receive.Path == "" {
		cfg.Push.Receive.Path = DefaultPushReceivePath
	}
	if cfg.Push.Send.Interval == 0 {
		cfg.Push.Send.Interval = Duration(30 * time.Second)
	}
}

// Validate runs schema-adjacent, cross-field sanity checks — the same checks
// `circa config check` (§8.1.2) runs before a restart — and returns every
// problem found, not just the first, so a bad config can be fixed in one
// pass instead of one error at a time.
func (cfg Config) Validate() []error {
	var errs []error

	for i, t := range cfg.Ingest.Scrape.Targets {
		if t.URL == "" {
			errs = append(errs, fmt.Errorf("ingest.scrape.targets[%d]: url is required", i))
		}
		if t.Interval <= 0 {
			errs = append(errs, fmt.Errorf("ingest.scrape.targets[%d] (%s): interval must be positive", i, t.URL))
		}
	}

	if cfg.Features.InfluxReceive && cfg.Ingest.Influx.Path == "" {
		errs = append(errs, errors.New("features.influx_receive is true but ingest.influx.path is empty"))
	}

	if cfg.Features.PushReceive && cfg.Push.Receive.Path == "" {
		errs = append(errs, errors.New("features.push_receive is true but push.receive.path is empty"))
	}

	if cfg.Features.PushSend {
		if cfg.Push.Send.URL == "" {
			errs = append(errs, errors.New("features.push_send is true but push.send.url is empty"))
		}
		if cfg.Push.Send.Interval <= 0 {
			errs = append(errs, errors.New("features.push_send is true but push.send.interval must be positive"))
		}
	}

	if cfg.Features.Backup {
		switch cfg.Backup.Mode {
		case "push", "pull":
		case "":
			errs = append(errs, errors.New("features.backup is true but backup.mode is not set (want \"push\" or \"pull\")"))
		default:
			errs = append(errs, fmt.Errorf("backup.mode %q is invalid (want \"push\" or \"pull\")", cfg.Backup.Mode))
		}
		if cfg.Backup.Mode == "pull" && cfg.Backup.Catalog.URI == "" {
			errs = append(errs, errors.New("backup.mode is pull but backup.catalog.uri is empty"))
		}
		if cfg.Backup.Catalog.Warehouse == "" {
			errs = append(errs, errors.New("features.backup is true but backup.catalog.warehouse is empty"))
		}
	}

	for user, hash := range cfg.Auth.Users {
		if user == "" {
			errs = append(errs, errors.New("auth.users: empty username is not allowed"))
			continue
		}
		if !looksBcrypt(hash) {
			errs = append(errs, fmt.Errorf("auth.users[%s]: value must be a bcrypt hash (got a plaintext-looking string) — use `circa auth add-user %s`", user, user))
		}
	}

	return errs
}

// Check loads and validates the YAML config at path, for `circa config
// check <file>` (§8.1.2). Unlike Load — where an absent path/file means
// "start with defaults," the right behavior for a normal run with no
// -config flag — Check requires the file to actually exist, since a typo'd
// path should fail loudly rather than silently validate the defaults.
func Check(path string) error {
	if path == "" {
		return errors.New("path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("checking config %s: %w", path, err)
	}
	_, err := Load(path)
	return err
}

// Redacted returns a copy of cfg safe to display externally — bcrypt hashes
// are masked, matching DESIGN/08 §8.3's "/status renders the effective
// config with secrets redacted." Used by httpapi's GET /status.
func (cfg Config) Redacted() Config {
	redacted := cfg
	if len(cfg.Auth.Users) > 0 {
		users := make(map[string]string, len(cfg.Auth.Users))
		for user := range cfg.Auth.Users {
			users[user] = "[redacted]"
		}
		redacted.Auth.Users = users
	}
	return redacted
}

var bcryptPrefix = regexp.MustCompile(`^\$2[aby]?\$`)

func looksBcrypt(hash string) bool {
	return bcryptPrefix.MatchString(hash)
}
