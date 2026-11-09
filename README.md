# goahead

Simple service that allows or denies server / OS restarts in a cluster-aware way.

Client can be found here: https://github.com/xorpaul/goahead_client

## Building

```sh
go mod tidy
go build
```

## Running

```sh
./goahead -config ./config.yml
```

Flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `./config.yml` | Path to the main config file |
| `-debug` | `false` | Enable debug logging |
| `-version` | | Print version and build time, then exit |

## Configuration

### Main config (`config.yml`)

```yaml
listen_address: 0.0.0.0
listen_port: 8443
log_base_dir: /var/log/goahead/
save_state_dir: /var/lib/goahead/
include_dir: /etc/goahead/clusters.d/
timeout: 20s
ssl_certificate_file: /etc/goahead/ssl/goahead.pem
ssl_private_key: /etc/goahead/ssl/goahead.key
ssl_client_cert_ca_file: /etc/goahead/ssl/ca.pem
ssl_require_and_verify_client_cert: false
sendmail_path: /usr/sbin/sendmail   # optional, default /usr/sbin/sendmail
mail_from: goahead@example.com      # optional, default goahead@<hostname>
cluster_config_url_base: https://git.example.com/org/goaheadmw/src/branch/main/files/cluster.conf.d/   # optional
```

`sendmail_path`, `mail_from` and `cluster_config_url_base` are only used for the `mail` entries of `reboot_completion_panic_actions`. The link in the mail is `cluster_config_url_base` plus the path of the cluster file relative to `include_dir`. When `include_dir` is the deployed content of `files/cluster.conf.d/live/` (goaheadmw), the base must end in `.../files/cluster.conf.d/live/`.

If `ssl_certificate_file` or `ssl_private_key` do not exist, goahead generates a self-signed certificate automatically on startup (useful for development and testing).

`ssl_client_cert_ca_file` is only required when `ssl_require_and_verify_client_cert: true`.

### Cluster config (`include_dir/*.yml`)

Each file in `include_dir` may contain one or more cluster definitions:

```yaml
foobar-server:
  enabled: true
  name_pattern: "^(foobar-server-).*[[:digit:]]{2}.(domain).(tld)$"
  blacklist_name_pattern:
    - ".*-standalone-.*"
    - ".*-black-.*"
  cluster_type: active/active        # or: standalone
  minimum_uptime: 24h
  allowed_parallel_restarts: 2

  reboot_goahead_actions:
    - /etc/goahead/hooks/notify_admins.sh {:%fqdn%:} {:%cluster%:}

  reboot_completion_check: /etc/goahead/checks/http_check.sh {:%fqdn%:}
  reboot_completion_check_interval: 15s
  reboot_completion_check_consecutive_successes: 3
  reboot_completion_check_offset: 15m
  reboot_completion_actions:
    - /etc/goahead/hooks/notify_admins.sh {:%fqdn%:} {:%cluster%:}

  reboot_completion_panic_threshold: 3h
  reboot_completion_panic_actions:
    mail:
      - ops@example.com
    scripts:
      - /etc/goahead/hooks/panic.sh {:%fqdn%:}

  raise_errors: false
```

The placeholders `{:%fqdn%:}` and `{:%cluster%:}` are substituted with the requesting host's FQDN and its matched cluster name at runtime.

## API

### Request or confirm a restart

```
POST /v1/request/restart/os
Content-Type: application/json

{"fqdn":"foobar-server1.domain.tld","uptime":"2255h27m43s"}
```

On the first call, goahead registers the request and returns `"go_ahead":false` with a `request_id`. The client must re-send with the same `request_id` to confirm and receive a final decision.

**Allowed:**
```json
{"timestamp":"2026-09-18T10:00:00Z","go_ahead":true,"unknown_host":false,"request_id":"BSporAsx","found_cluster":"foobar-server","requesting_fqdn":"foobar-server1.domain.tld","message":"","reported_uptime":"2255h27m43s"}
```

**Denied** (too many parallel restarts already in progress):
```json
{"timestamp":"2026-09-18T10:00:00Z","go_ahead":false,"unknown_host":false,"request_id":"BSporAsx","found_cluster":"foobar-server","requesting_fqdn":"foobar-server1.domain.tld","message":"Denied restart request as the current_ongoing_restarts of cluster foobar-server is larger than the allowed_parallel_restarts: 2 >= 2 Currently restarting hosts: foobar-server2.domain.tld,foobar-server3.domain.tld","reported_uptime":"2255h27m43s"}
```

### Inquire without requesting

```
POST /v1/inquire/restart/
Content-Type: application/json

{"fqdn":"foobar-server1.domain.tld","uptime":"2255h27m43s"}
```

Returns the same response shape as above but never grants a restart or triggers hooks — useful for checking current cluster state without committing.

## Workflow

1. **Client requests restart** — sends FQDN and uptime to `/v1/request/restart/os`.
2. **goahead decides** — matches FQDN against cluster name patterns, checks `minimum_uptime`, and counts ongoing restarts against `allowed_parallel_restarts`.
3. **Allowed** — goahead runs any `reboot_goahead_actions`, marks the host as restarting in the cluster state, and returns `"go_ahead":true`.
4. **Client reboots** — the client acts on the approval and reboots.
5. **Completion check** — when the host contacts goahead again with low uptime, the `reboot_completion_check` command is polled every `reboot_completion_check_interval` until it succeeds `reboot_completion_check_consecutive_successes` times.
6. **Completion actions** — once the check passes, `reboot_completion_actions` run, the host is removed from the restarting set, and the cluster slot is freed for the next host.
7. **Panic threshold** — if a host has not successfully rebooted within `reboot_completion_panic_threshold` after the last goahead, `reboot_completion_panic_actions` are triggered. A background timer checks this every 30 seconds, so it fires even if no further requests arrive; a later request from another host can trigger it as well. After a panic the actions repeat at most once per threshold. For every host that is still offline goahead re-runs `reboot_completion_check`, runs the `scripts` (with `{:%fqdn%:}` being the offline host) and mails the full command lines, exit codes and output to the `mail` recipients using the local `sendmail`, including a link to the cluster config file. A threshold of `0` or unset disables the panic. A cluster whose `reboot_completion_check_offset` is larger than `reboot_completion_panic_threshold` is automatically disabled at startup with an ERROR in the main and cluster log. Besides the `mail:`/`scripts:` map, the list format used by the goaheadmw cluster configs is accepted as `reboot_completion_panic_action:` (singular): a list of single-key maps where `mail` holds addresses and every other key (`script1`, `script2`, ...) holds commands.

## Testing

```sh
go test -v
```

The test server listens on `listen_port` of `./config.yml` (8443) by default and uses a private temporary directory for its state and logs, so the tests never touch `/tmp/goahead/`. If that port is taken, choose another one with `go test -goahead-test-port=9443`, with the environment variable `GOAHEAD_TEST_PORT=9443`, or `0` for any free port:

```sh
go test -v -goahead-test-port=0
```

No pre-existing SSL certificates are required — goahead generates a self-signed certificate into `./ssl/` automatically if the files are missing.
