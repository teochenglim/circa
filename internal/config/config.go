// Package config loads circa's single YAML config file.
//
// server, ingest.scrape, storage, push, auth, alerting, and anomaly are read
// and acted on as of v0.4.0. backup is decoded into Config (so `circa config
// check` has something to validate) but not yet acted on — that lands in
// v0.5.0.
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
	Anomaly  Anomaly  `yaml:"anomaly"`
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

// Alerting is read and acted on as of v0.4.0 (DESIGN/06 §6.1) when
// features.alerts is true.
type Alerting struct {
	Rules     []AlertRule      `yaml:"rules"`
	Notifiers []NotifierConfig `yaml:"notifiers"`
}

// AlertRule is one rule: a metric selector (Metric + Labels, exact-match —
// same convention as the query API) plus a Condition, a hysteresis window
// (For), and a Severity. For is duration-based (Prometheus's own `for:`
// convention — "condition must hold continuously for at least this long
// before firing"), not the "N evaluations" phrasing in DESIGN/06 §6.1:
// evaluation cadence varies per scrape target's interval, so a wall-clock
// duration is the more portable knob, and it's a convention Prometheus users
// already know.
type AlertRule struct {
	Name      string            `yaml:"name"`
	Metric    string            `yaml:"metric"`
	Labels    map[string]string `yaml:"labels"`
	Condition ConditionConfig   `yaml:"condition"`
	For       Duration          `yaml:"for"`
	Severity  string            `yaml:"severity"` // info | warning | critical
	Notify    []string          `yaml:"notify"`   // notifier names to dispatch to; empty = every configured notifier
}

// ConditionConfig is one rule's trigger condition — threshold and
// rate_of_change compare Value against the sample (rate_of_change compares
// against the per-second rate of change over Window); anomaly fires when the
// storage-embedded anomaly bit (§6.2) is set and ignores Operator/Value/Window.
type ConditionConfig struct {
	Type     string   `yaml:"type"` // threshold | rate_of_change | anomaly
	Operator string   `yaml:"operator"`
	Value    float64  `yaml:"value"`
	Window   Duration `yaml:"window"` // rate_of_change only
}

// NotifierConfig is one configured notification channel (DESIGN/06 §6.1:
// "pluggable interface, start with generic webhook + Slack").
type NotifierConfig struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // webhook | slack
	URL  string `yaml:"url"`
}

// Anomaly configures the k-means ensemble (DESIGN/06 §6.2), read only when
// features.ml is true. It lives in its own top-level section (not nested
// under features.ml, which stays a bool) to mirror alerting/backup/push's
// "flag toggles the feature, its own section holds the knobs" shape. Field
// names and defaults deliberately mirror Netdata's own ml_config.cc — see
// DESIGN/10_ml_summary.md §4 for the full mapping; this isn't a from-scratch
// design, it's matched against the real implementation.
type Anomaly struct {
	ModelCount      int      `yaml:"model_count"`      // ensemble size per metric (Netdata default: 18)
	TrainingWindow  Duration `yaml:"training_window"`  // history each new model trains on
	RetrainInterval Duration `yaml:"retrain_interval"` // how often a new model is trained and added to the ensemble
	DiffN           int      `yaml:"diff_n"`           // order of differencing (0 or 1) applied before smoothing
	SmoothN         int      `yaml:"smooth_n"`         // rolling-average window applied after differencing
	LagN            int      `yaml:"lag_n"`            // lagged values included per feature vector
	ScoreThreshold  float64  `yaml:"score_threshold"`  // 0..100 - min-max-normalized distance at/above which a point counts as anomalous
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

// Anomaly defaults — mirror Netdata's own ml_config.cc defaults exactly
// (see DESIGN/10_ml_summary.md §4), not just DESIGN/06 §6.2's paraphrase of
// them: 18 models per dimension, retrained every 3h on a 6h window,
// diff/smooth/lag of 1/3/5, threshold 99 (of 100).
const (
	DefaultModelCount      = 18
	DefaultTrainingWindow  = 6 * time.Hour
	DefaultRetrainInterval = 3 * time.Hour
	DefaultDiffN           = 1
	DefaultSmoothN         = 3
	DefaultLagN            = 5
	DefaultScoreThreshold  = 99.0
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
		Anomaly: Anomaly{
			ModelCount:      DefaultModelCount,
			TrainingWindow:  Duration(DefaultTrainingWindow),
			RetrainInterval: Duration(DefaultRetrainInterval),
			DiffN:           DefaultDiffN,
			SmoothN:         DefaultSmoothN,
			LagN:            DefaultLagN,
			ScoreThreshold:  DefaultScoreThreshold,
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
	if cfg.Anomaly.ModelCount == 0 {
		cfg.Anomaly.ModelCount = DefaultModelCount
	}
	if cfg.Anomaly.TrainingWindow == 0 {
		cfg.Anomaly.TrainingWindow = Duration(DefaultTrainingWindow)
	}
	if cfg.Anomaly.RetrainInterval == 0 {
		cfg.Anomaly.RetrainInterval = Duration(DefaultRetrainInterval)
	}
	if cfg.Anomaly.LagN == 0 {
		cfg.Anomaly.LagN = DefaultLagN
	}
	if cfg.Anomaly.ScoreThreshold == 0 {
		cfg.Anomaly.ScoreThreshold = DefaultScoreThreshold
	}
	// DiffN and SmoothN are deliberately not defaulted here: 0 is a valid,
	// meaningful value for both (matching Netdata's own semantics — diff_n:
	// 0 disables differencing, smooth_n: 0 disables smoothing), unlike a
	// duration or count of 0, which is never meaningful and safe to treat as
	// "unset." Only Default() (no config file at all) sets them to 1/3.
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

	if cfg.Features.Alerts {
		errs = append(errs, cfg.validateAlerting()...)
	}
	if cfg.Features.ML {
		errs = append(errs, cfg.validateAnomaly()...)
	}

	return errs
}

var validOperators = map[string]bool{">": true, "<": true, ">=": true, "<=": true, "==": true, "!=": true}
var validSeverities = map[string]bool{"": true, "info": true, "warning": true, "critical": true}

func (cfg Config) validateAlerting() []error {
	var errs []error

	notifierNames := make(map[string]bool, len(cfg.Alerting.Notifiers))
	for i, n := range cfg.Alerting.Notifiers {
		if n.Name == "" {
			errs = append(errs, fmt.Errorf("alerting.notifiers[%d]: name is required", i))
		} else if notifierNames[n.Name] {
			errs = append(errs, fmt.Errorf("alerting.notifiers[%d]: duplicate name %q", i, n.Name))
		}
		notifierNames[n.Name] = true

		switch n.Type {
		case "webhook", "slack":
		default:
			errs = append(errs, fmt.Errorf("alerting.notifiers[%d] (%s): type %q is invalid (want \"webhook\" or \"slack\")", i, n.Name, n.Type))
		}
		if n.URL == "" {
			errs = append(errs, fmt.Errorf("alerting.notifiers[%d] (%s): url is required", i, n.Name))
		}
	}

	for i, r := range cfg.Alerting.Rules {
		if r.Name == "" {
			errs = append(errs, fmt.Errorf("alerting.rules[%d]: name is required", i))
		}
		if r.Metric == "" {
			errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): metric is required", i, r.Name))
		}
		if r.For < 0 {
			errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): for must not be negative", i, r.Name))
		}
		if !validSeverities[r.Severity] {
			errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): severity %q is invalid (want \"info\", \"warning\", or \"critical\")", i, r.Name, r.Severity))
		}

		switch r.Condition.Type {
		case "threshold":
			if !validOperators[r.Condition.Operator] {
				errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): condition.operator %q is invalid", i, r.Name, r.Condition.Operator))
			}
		case "rate_of_change":
			if !validOperators[r.Condition.Operator] {
				errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): condition.operator %q is invalid", i, r.Name, r.Condition.Operator))
			}
			if r.Condition.Window <= 0 {
				errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): condition.window must be positive for rate_of_change", i, r.Name))
			}
		case "anomaly":
			// operator/value/window unused - nothing further to validate
		default:
			errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): condition.type %q is invalid (want \"threshold\", \"rate_of_change\", or \"anomaly\")", i, r.Name, r.Condition.Type))
		}
		if r.Condition.Type == "anomaly" && !cfg.Features.ML {
			errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): condition.type is \"anomaly\" but features.ml is false — the anomaly bit is never set", i, r.Name))
		}

		for _, name := range r.Notify {
			if !notifierNames[name] {
				errs = append(errs, fmt.Errorf("alerting.rules[%d] (%s): notify references unknown notifier %q", i, r.Name, name))
			}
		}
	}

	return errs
}

// validateAnomaly's bounds mirror ml_config.cc's own clamp ranges (see
// DESIGN/10_ml_summary.md §4) rather than arbitrary limits — e.g. diff_n and
// lag_n's ranges match what Netdata's k-means feature pipeline itself
// requires to produce a sane feature vector.
func (cfg Config) validateAnomaly() []error {
	var errs []error
	if cfg.Anomaly.ModelCount < 1 {
		errs = append(errs, errors.New("features.ml is true but anomaly.model_count must be at least 1"))
	}
	if cfg.Anomaly.TrainingWindow <= 0 {
		errs = append(errs, errors.New("features.ml is true but anomaly.training_window must be positive"))
	}
	if cfg.Anomaly.RetrainInterval <= 0 {
		errs = append(errs, errors.New("features.ml is true but anomaly.retrain_interval must be positive"))
	}
	if cfg.Anomaly.DiffN < 0 || cfg.Anomaly.DiffN > 1 {
		errs = append(errs, errors.New("features.ml is true but anomaly.diff_n must be 0 or 1"))
	}
	if cfg.Anomaly.SmoothN < 0 {
		errs = append(errs, errors.New("features.ml is true but anomaly.smooth_n must not be negative"))
	}
	if cfg.Anomaly.LagN < 1 {
		errs = append(errs, errors.New("features.ml is true but anomaly.lag_n must be at least 1"))
	}
	if cfg.Anomaly.ScoreThreshold <= 0 || cfg.Anomaly.ScoreThreshold > 100 {
		errs = append(errs, errors.New("features.ml is true but anomaly.score_threshold must be in (0,100]"))
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
// and notifier webhook URLs (which routinely embed a bearer token, e.g.
// Slack's incoming-webhook path) are masked, matching DESIGN/08 §8.3's
// "/status renders the effective config with secrets redacted." Used by
// httpapi's GET /status.
func (cfg Config) Redacted() Config {
	redacted := cfg
	if len(cfg.Auth.Users) > 0 {
		users := make(map[string]string, len(cfg.Auth.Users))
		for user := range cfg.Auth.Users {
			users[user] = "[redacted]"
		}
		redacted.Auth.Users = users
	}
	if len(cfg.Alerting.Notifiers) > 0 {
		notifiers := make([]NotifierConfig, len(cfg.Alerting.Notifiers))
		for i, n := range cfg.Alerting.Notifiers {
			n.URL = "[redacted]"
			notifiers[i] = n
		}
		redacted.Alerting.Notifiers = notifiers
	}
	return redacted
}

var bcryptPrefix = regexp.MustCompile(`^\$2[aby]?\$`)

func looksBcrypt(hash string) bool {
	return bcryptPrefix.MatchString(hash)
}
