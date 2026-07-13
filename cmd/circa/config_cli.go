package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/teochenglim/circa/internal/config"
)

// runConfigInit implements `circa config init` (DESIGN/08 §8.1.2): writes a
// fully-commented, ready-to-run config.yaml, optionally customized by
// --profile and a handful of targeted overrides.
func runConfigInit(args []string) error {
	fs := flag.NewFlagSet("config init", flag.ExitOnError)
	profile := fs.String("profile", "minimal", `template preset: "minimal" (collection+storage+UI only) or "full" (everything on, for demo/eval)`)
	output := fs.String("output", "config.yaml", "path to write the generated config to")
	hostname := fs.String("hostname", "", "host:port for the default scrape target (default localhost:9100)")
	listen := fs.String("listen", "", "server.listen_address override (default :9100)")
	retentionRaw := fs.String("retention.raw", "", "storage.retention.raw override (default 2h)")
	retentionMinute := fs.String("retention.minute", "", "storage.retention.minute override (default 7d)")
	retentionHour := fs.String("retention.hour", "", "storage.retention.hour override (default 365d)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(*output); err == nil {
		return fmt.Errorf("%s already exists — remove it first or pass -output to write elsewhere", *output)
	}

	out, err := config.GenerateTemplate(config.TemplateOptions{
		Profile:         *profile,
		ListenAddress:   *listen,
		Hostname:        *hostname,
		RetentionRaw:    *retentionRaw,
		RetentionMinute: *retentionMinute,
		RetentionHour:   *retentionHour,
	})
	if err != nil {
		return err
	}

	if err := os.WriteFile(*output, []byte(out), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", *output, err)
	}
	fmt.Printf("wrote %s (profile: %s)\n", *output, *profile)
	return nil
}

// runConfigCheck implements `circa config check <file>` (DESIGN/08 §8.1.2):
// schema + cross-field validation before a restart, mirroring `promtool
// check config`.
func runConfigCheck(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: circa config check <file>")
	}
	if err := config.Check(args[0]); err != nil {
		return err
	}
	fmt.Printf("%s: OK\n", args[0])
	return nil
}

func runConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: circa config <init|check> ...")
	}
	switch args[0] {
	case "init":
		return runConfigInit(args[1:])
	case "check":
		return runConfigCheck(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q (want \"init\" or \"check\")", args[0])
	}
}
