# systemd unit

For a bare-metal/VM host — the direct alternative to the Docker/Kubernetes paths in the root [README.md](../../README.md), for whoever isn't containerizing this node at all. Circa is a per-node local agent either way (see [../../ARCHITECTURE.md](../../ARCHITECTURE.md) "Deployment shape"): one `circa` process per host, reading that host's own `/proc`/`/sys` directly.

## Install

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin circa
sudo install -m 0755 bin/circa /usr/local/bin/circa
sudo mkdir -p /etc/circa /var/lib/circa
sudo cp config.example.yaml /etc/circa/config.yaml   # then edit it — see README.md "Configuration"
sudo chown -R circa:circa /var/lib/circa
sudo cp packaging/systemd/circa.service /etc/systemd/system/circa.service
sudo systemctl daemon-reload
sudo systemctl enable --now circa
```

`storage.path` in `/etc/circa/config.yaml` should point under `/var/lib/circa` (the unit's `StateDirectory`, owned by the `circa` user) — e.g. `/var/lib/circa/data`.

## Operating

```bash
systemctl status circa
journalctl -u circa -f
sudo systemctl restart circa   # after any config.yaml change — see DESIGN/08 §8.1.3
                                # for which fields are safe to reload vs. require a restart
```

`circa config check /etc/circa/config.yaml` (§8.1.2) before restarting catches most bad edits ahead of time.

The unit runs as an unprivileged dedicated `circa` user with `ProtectSystem=strict`/`NoNewPrivileges`/a narrow `ReadWritePaths` — the same hardening shape node_exporter's own packaged unit uses. No capability is needed even for `features.collect`: everything it reads (`/proc`, `/sys`, `sysctl`) is world-readable.
