# Circa — Design 02: High-level architecture

```
┌───────────────────────────────────────────────────────────────────────┐
│                         Circa (single binary)                          │
│                                                                          │
│  ┌───────────┐   ┌───────────┐   ┌───────────┐   ┌──────────┐          │
│  │ Collectors │──▶│  Ingest   │──▶│  RRD-store │──▶│  Query   │          │
│  │ (imported  │   │  pipeline │   │ (ring buf  │   │  engine  │          │
│  │  from      │   │ (label +  │   │  + tiered  │   │ (range,  │          │
│  │  node_     │   │  downsamp)│   │  downsample│   │  instant)│          │
│  │  exporter) │   └───────────┘   │  on disk)  │   └────┬─────┘          │
│  └───────────┘         ▲           └───────────┘        │                │
│        ▲               │ push (remote_write)             ▼                │
│        │          ┌───────────┐                    ┌──────────┐          │
│  ┌───────────┐    │  Receiver  │                    │ HTTP mux │          │
│  │  ML engine │◀───┤  /api/v1/  │                    │  /metrics│          │
│  │  (feature- │    │  write     │                    │  /api/v1 │          │
│  │  flagged)  │    └───────────┘                    │  /       (UI)      │
│  └─────┬──────┘                                      │  /status (config) │
│        ▼                                              │  /healthz,readyz │
│  ┌───────────┐                                        └──────────┘       │
│  │  Alert     │──▶ webhook / Slack / email / PagerDuty-compat            │
│  │  engine    │                                                          │
│  │ (feature-  │        ┌───────────┐                                    │
│  │  flagged)  │        │ Auth (opt-│  no-auth (default) / bcrypt         │
│  └───────────┘         │ in, bcrypt)│  multi-user basic auth             │
│                          └───────────┘                                   │
│  ┌───────────┐   push ──▶ external remote_write receiver                 │
│  │  Backup    │   pull ──▶ central backup agent scrapes this node's      │
│  │  (feature- │            backup-range API on its own schedule          │
│  │  flagged)  │──▶ Iceberg lake (CDC-style delta append)                 │
│  └───────────┘                                                           │
└───────────────────────────────────────────────────────────────────────┘
     Single YAML config file with feature flags controls every
     optional block above. Static HTML/JS/CSS embedded via go:embed.
```

Everything runs in one process, one goroutine tree, one binary. No external dependencies at runtime (no Redis, no Postgres, no separate TSDB). Every block past "Collectors → Ingest → RRD-store → Query engine → `/metrics` + UI" is optional and off by default.

See [ARCHITECTURE.md](../ARCHITECTURE.md) for the intended Go package layout this diagram maps to, and how a single collection tick flows through the code.
