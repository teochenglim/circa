# Circa — Design 08: Configuration, authentication, and operability

## 8.1 Configuration: single YAML file, feature flags, and a CLI for quick setup

### 8.1.1 Single config file

Everything — server settings, ingestion (scrape targets, line-protocol/remote-write receivers), storage/retention, feature flags, alerting rules, backup/Iceberg settings, auth — lives in one YAML file (e.g. `config.yaml`), not scattered across flags and a separate file the way Prometheus splits web/auth config. This is a deliberate simplification: a single file is easier to version-control, easier to reason about, and matches what was explicitly asked for.

See [config.example.yaml](../config.example.yaml) at the repo root for the full sketch — server, features, ingestion, storage retention, alerting, backup, and auth all in one place.

### 8.1.2 CLI for quick templated config

- `circa config init` — generates a fully-commented, ready-to-run YAML template. Support flag-driven quick customization so first-run doesn't mean reading 200 lines of comments: e.g. `circa config init --profile minimal` (collection + storage + UI only, everything else off) vs `--profile full` (everything on, for evaluation/demo purposes), plus targeted overrides like `--hostname`, `--retention.raw=1h`, `--listen=:9100`.
- `circa config check <file>` — validates the YAML (schema, cross-field sanity like "backup.mode is pull but no catalog URI set") before a restart, mirroring `promtool check config` / `promtool check web-config`. This catches typos before they become an outage — a genuinely common, avoidable pain point across a fleet of agents.
- `circa auth add-user <name>` / `circa auth reset-password <name>` — prompts for a password, bcrypt-hashes it, writes it into the config (or a referenced auth file). This is the CLI-driven answer to password management (see §8.2) rather than a web-based self-service flow.

### 8.1.3 Feature flags are the leanness mechanism

The reason feature flags matter as much as the "lean CPU/memory" requirement itself: a baseline Circa with only ingestion (scrape client) + storage + `/metrics` + UI should cost no more than node_exporter itself plus a small ring-buffer — Circa's own process never parses `/proc`/`/sys`, so its baseline footprint is that of an HTTP client and a compact storage engine, not a full collector suite. Every optional capability — ML, alerting, backup, push send/receive — is opt-in and independently toggleable, and the docs should state each one's rough resource cost so enabling something is an informed choice, not a surprise. Config that's safe to hot-reload (alert rules, auth users, feature-flag toggles that don't touch storage layout) should reload on `SIGHUP` without a restart; config that affects storage layout or listen address should require a restart and say so clearly, rather than silently doing something unsafe on reload.

## 8.2 Authentication and being honest about what's worth building

### 8.2.1 Defaults and multi-user basic auth

- **Default: no authentication.** This matches node_exporter's and Prometheus's own default, and is the right default for something meant to run on every node in a fleet on a trusted internal network — auth should be opt-in per deployment, not mandatory friction.
- **When enabled: multi-user, bcrypt-hashed basic auth**, directly modeled on Prometheus's own proven approach — exporter-toolkit's web config supports a list of usernames and bcrypt-hashed passwords with full access via basic authentication; if the list is empty, no auth is required, and the config is explicitly described as meant for simple use cases with a few users, which is exactly this tool's scale. Multiple users are supported by simply having multiple entries in the `auth.users` map (§8.1) — reuse this pattern rather than inventing a new one.
- **Be honest about what basic auth gives you**: it's authentication (who can log in), not authorization (what they're allowed to do) — every user in the list gets full access. If genuinely different privilege levels are needed, that's a real, separate RBAC feature to decide on deliberately — don't assume "multiple users" implies "multiple permission levels" without confirming that's actually wanted.
- **Session model: stay stateless.** Basic auth re-sends credentials on every request; there's no session cookie, no server-side session store, no CSRF surface to defend. Recommend keeping it stateless for v1 and revisiting only if there's a concrete reason.

### 8.2.2 Password reset — be critical here

A true self-service "remote password reset" (emailed link, expiring token) is meaningfully larger than it sounds — SMTP configuration, a token store with expiry, rate-limiting/brute-force protection on a new unauthenticated attack surface, and it turns a lean single-binary monitoring agent into something that also has to reason about account-recovery security.

**Recommendation for v1**: skip self-service reset entirely. Provide `circa auth reset-password <user>` (§8.1.2), run locally or over SSH by whoever administers the box. This doesn't lower the security bar — anyone who can run that command can already edit the YAML config directly, so it adds no new privilege, just a friendlier way to exercise one that already exists.

### 8.2.3 What NOT to build, and what to point people to instead

For anything beyond "a few trusted users with a shared login," the honest answer — and the one Prometheus's own documentation gives — is: basic authentication is meant for simple use cases, with a few users; put a reverse proxy in front for anything more (oauth2-proxy, Caddy's forward-auth, Tailscale/Teleport, an internal SSO gateway). Building real RBAC, SSO integration, or a user-management UI into Circa itself would work directly against the "lean, single-binary, simple" goals — worth stating this explicitly in the docs rather than leaving it implicit.

## 8.3 Config visibility and other operability features

- **`/status` page (mirrors Prometheus's own `/config`/`/flags`/`/status` pages)**: render the effective, merged configuration (defaults + YAML file + any CLI overrides) as read-only YAML, with secrets redacted — bcrypt hashes shown as `[redacted]`, S3/catalog credentials masked, any webhook URL with an embedded token masked.
- **`/healthz` and `/readyz`**: trivial to add, meaningfully useful for systemd/Docker/Kubernetes liveness and readiness probes.
- **`/version` / build-info endpoint**: version, commit, build date — useful for fleet inventory ("which nodes are still on the old build").
- **Self-metrics, surfaced honestly**: expose Circa's own CPU/RSS/goroutine count both as `circa_*` series on `/metrics` and as a small panel in the dashboard UI. Given "lean CPU/memory" is a stated goal, let operators verify that directly rather than take it on faith — especially valuable once ML/alerts/backup are toggled on, since each has a real, non-zero cost.
- **A soft memory ceiling**: honor `GOMEMLIMIT` and/or expose a `--max-memory` flag, so a tool whose whole pitch is being lightweight has a backstop against a misconfigured ML training window or an unbounded backup queue quietly growing past what was intended.
- **GitOps-friendly by construction**: because config is one YAML file, it version-controls naturally — `circa config check` in CI before rollout, config in Git, matches FluxCD-style GitOps patterns.
- **Explicit non-goals for v1**: no plugin marketplace, no multi-tenant SaaS-style UI, no built-in RBAC. Each would cut against "lean, single-binary, simple" — better to say so plainly than let scope creep in one feature at a time.
