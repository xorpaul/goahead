# goahead
Simple service that allows or denies server / OS restarts.

Client can be found here: https://github.com/xorpaul/goahead_client

## Building

```sh
go mod tidy
go build
```

### workflow

#### Client inquires or requests restart
Client sends request to service with URI `/v1/request/restart/os` with JSON payload:
```
{"fqdn":"foobar-server1.domain.tld","uptime":"2255h27m43s"}
```

#### `goahead` service checks and decides if client may restart

The `fqdn` from the client payload gets matched against definied cluster nodes name patterns in cluster config:

```
---
foobar-server:
  enabled: true
  name_pattern: "^(foobar-server-).*[[:digit:]]{2}.(domain).(tld)$"
  blacklist_name_pattern:
    - ".*-standalone-.*"
    - ".*-black-.*"
  minimum_uptime: 24h
  cluster_type: active/active
  allowed_parallel_restarts: 2
  reboot_goahead_actions:
    - /etc/goahead/goahead_hooks.d/notify_admins.sh {:%fqdn%:} {:%cluster%:}
  reboot_completion_check: /etc/goahead/reboot_completion_checks.d/check.sh {:%fqdn%:}
  reboot_completion_check_interval: 15s
  reboot_completion_check_consecutive_successes: 3
  reboot_completion_check_offset: 15m
  reboot_completion_actions:
    - /etc/goahead/goahead_hooks.d/notify_admins.sh {:%fqdn%:} {:%cluster%:}
  reboot_completion_panic_threshold: 3h
```

Either allows a restart with `"go_ahead":true`:
```
{"timestamp":"2018-11-22T15:06:35.538017828Z","go_ahead":true,"unknown_host":false,"request_id":"BSporAsx","found_cluster":"foobar-server","requesting_fqdn":"foobar-server1.domain.tld","message":"","reported_uptime":"2255h27m43s"}
```

or denies a restart with the reason:
```
{"timestamp":"2018-11-22T15:06:35.538017828Z","go_ahead":false,"unknown_host":false,"request_id":"BSporAsx","found_cluster":"foobar-server","requesting_fqdn":"foobar-server1.domain.tld","message":"Denied restart request as the current_ongoing_restarts of cluster foobar-server is larger than the allowed_parallel_restarts: 1 >= 1 Currently restarting hosts: foobar-server2.domain.tld","reported_uptime":"2255h27m43s"}
```

#### Client processes the response

If the restart request was denied via `"go_ahead":false` then the client terminates and will/should ask again later.

If the restart was allowed then optionally the `goahead` service and/or the client can trigger certian hooks.
E.g. Clean removal from load-balancing/cluster or notify monitoring of upcoming restart etc.


#### `goahead` service checks for successful restart

The configured `reboot_completion_check` gets triggered, when the first contact from the previous client gets recieved.
When the check returns with the expected return code for the configured `reboot_completion_check_consecutive_successes` times, then the client is considered as successfully rebooted and the amount of currently restarting cluster nodes is decremented. 
