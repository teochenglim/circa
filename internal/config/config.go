// Package config loads circa's single YAML config file.
//
// v0.1.0 only reads the fields the core ingestion+storage pipeline needs
// (server, ingest.scrape, storage). Later milestones (features, alerting,
// backup, push, auth) already exist in config.example.yaml and are decoded
// into Config so `circa config check` (v0.3.0) has something to validate,
// but nothing in this package acts on them yet.
package config

import (
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
	}
}

// Load reads and parses the YAML config at path. An empty path returns
// Default() unchanged — `circa` should run with no config file present.
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

	if cfg.Server.ListenAddress == "" {
		cfg.Server.ListenAddress = Default().Server.ListenAddress
	}
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = Default().Storage.Path
	}
	if cfg.Storage.Retention.Raw == 0 {
		cfg.Storage.Retention.Raw = Default().Storage.Retention.Raw
	}

	for i, t := range cfg.Ingest.Scrape.Targets {
		if t.URL == "" {
			return Config{}, fmt.Errorf("ingest.scrape.targets[%d]: url is required", i)
		}
		if t.Interval <= 0 {
			return Config{}, fmt.Errorf("ingest.scrape.targets[%d] (%s): interval must be positive", i, t.URL)
		}
	}

	return cfg, nil
}
