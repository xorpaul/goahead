package main

import (
	"fmt"
	"html/template"
	"time"
)

var uiFuncs = template.FuncMap{
	"ts": func(t time.Time) string {
		if t.IsZero() || t.Year() < 2000 {
			return "—"
		}
		return t.Local().Format("2006-01-02 15:04:05 MST")
	},
	"ago": func(t time.Time) string {
		if t.IsZero() || t.Year() < 2000 {
			return ""
		}
		return humanDuration(time.Since(t)) + " ago"
	},
	"dur": func(d time.Duration) string {
		if d <= 0 {
			return "—"
		}
		return humanDuration(d)
	},
	"until": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return "in " + humanDuration(time.Until(t))
	},
	"hosts": func(h []string) string {
		if len(h) == 0 {
			return "—"
		}
		s := h[0]
		for _, x := range h[1:] {
			s += ", " + x
		}
		return s
	},
	"hostsByClass": func(hs []hostRow, class string) []hostRow {
		var out []hostRow
		for _, h := range hs {
			if h.Class == class {
				out = append(out, h)
			}
		}
		return out
	},
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	d = d.Round(time.Second)
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// every value is escaped by html/template, check output and host names come from outside
var uiTemplates = template.Must(template.New("ui").Funcs(uiFuncs).Parse(`
{{define "head"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>goahead — {{.Page}}</title>
<style>
:root{--bg:#f6f7f9;--fg:#1c2128;--mut:#667085;--card:#fff;--line:#dde1e7;--ok:#1a7f4b;--warn:#a15c00;--bad:#c0262d;--info:#1f5fbf;--bad-bg:#fdecec;--warn-bg:#fff4e0;--ok-bg:#e6f5ec;--info-bg:#e8f0fc}
@media (prefers-color-scheme:dark){:root{--bg:#14171c;--fg:#e4e7ec;--mut:#98a2b3;--card:#1c2027;--line:#2d333d;--ok:#4cc38a;--warn:#e5a443;--bad:#f27278;--info:#6ea4f4;--bad-bg:#3a1d20;--warn-bg:#3a2c16;--ok-bg:#16301f;--info-bg:#18283f}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
header{background:var(--card);border-bottom:1px solid var(--line);padding:10px 16px;display:flex;flex-wrap:wrap;gap:6px 20px;align-items:center}
header b{font-size:16px}nav a{color:var(--mut);text-decoration:none;padding:4px 10px;border-radius:6px}nav a.on,nav a:hover{background:var(--info-bg);color:var(--info)}
main{padding:16px;max-width:1500px;margin:0 auto}h1{font-size:20px;margin:0 0 12px}h2{font-size:15px;margin:22px 0 8px}
.cards{display:flex;flex-wrap:wrap;gap:10px;margin-bottom:6px}.card{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:10px 14px;min-width:130px}
.card .n{font-size:24px;font-weight:600}.card .l{color:var(--mut);font-size:12px}
.tw{overflow-x:auto;background:var(--card);border:1px solid var(--line);border-radius:8px}
table{border-collapse:collapse;width:100%}th,td{padding:6px 10px;text-align:left;border-bottom:1px solid var(--line);vertical-align:top}th{font-size:12px;color:var(--mut);font-weight:600;white-space:nowrap}tr:last-child td{border-bottom:0}
a{color:var(--info)}.mut{color:var(--mut)}.mono,code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px}
pre{margin:0;white-space:pre-wrap;word-break:break-all}
.pill{display:inline-block;padding:1px 9px;border-radius:99px;font-size:12px;font-weight:600;white-space:nowrap}
.panic,.stuck,.unreadable{background:var(--bad-bg);color:var(--bad)}.restarting,.waiting,.polling,.sleeping{background:var(--warn-bg);color:var(--warn)}
.idle,.completed{background:var(--ok-bg);color:var(--ok)}.disabled,.stopped{background:var(--line);color:var(--mut)}
.note{background:var(--info-bg);color:var(--fg);border-radius:8px;padding:8px 12px;margin:8px 0}.none{padding:14px;color:var(--ok)}
.log{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:10px;max-height:420px;overflow:auto}
</style></head><body>
<header><b>goahead</b><nav>
<a href="/ui/"{{if eq .Page "overview"}} class="on"{{end}}>Clusters</a>
<a href="/ui/panic"{{if eq .Page "panic"}} class="on"{{end}}>Panicked hosts{{if .Snap.NPanic}} ({{.Snap.NPanic}}){{end}}</a>
<a href="/ui/activity"{{if eq .Page "activity"}} class="on"{{end}}>General activity</a>
<a href="/ui/status.json">JSON</a></nav>
<span class="mut" style="margin-left:auto">v{{.Snap.Version}} · up {{dur .Snap.Uptime}} · page {{ts .Snap.Generated}} · refreshes every 30s</span></header><main>{{end}}

{{define "foot"}}</main></body></html>{{end}}

{{define "statuspill"}}<span class="pill {{.}}">{{.}}</span>{{end}}

{{define "overview"}}{{template "head" .}}
<h1>Clusters</h1>
<div class="cards">
<div class="card"><div class="n">{{len .Snap.Clusters}}</div><div class="l">clusters</div></div>
<div class="card"><div class="n">{{.Snap.NPanic}}</div><div class="l">panicked</div></div>
<div class="card"><div class="n">{{.Snap.NStuck}}</div><div class="l">stuck, no panic threshold</div></div>
<div class="card"><div class="n">{{.Snap.NRestart}}</div><div class="l">restarting</div></div>
<div class="card"><div class="n">{{.Snap.NDisabled}}</div><div class="l">disabled</div></div>
<div class="card"><div class="n">{{len .Snap.Hosts}}</div><div class="l">hosts waited for</div></div>
</div>
<div class="note">goahead started {{ts .Snap.Start}}; request counters and check attempts count from then. The cluster logs are one file per cluster in the log directory, the tail of each is on its cluster page.</div>
<h2>All clusters (problems first)</h2>
<div class="tw"><table><tr><th>Cluster</th><th>Status</th><th>Restarting now</th><th>Last restart given</th><th>Last completed restart</th><th>Last panic</th><th>Requests / granted / denied</th><th>Last request</th></tr>
{{range .Snap.Clusters}}<tr>
<td><a href="/ui/cluster/{{.Name}}">{{.Name}}</a></td>
<td>{{template "statuspill" .Status}}{{if .StatusNote}}<div class="mut">{{.StatusNote}}</div>{{end}}</td>
<td class="mono">{{hosts .Restarting}}{{if .Restarting}}<div class="mut">{{.Ongoing}} of {{.Parallel}} allowed</div>{{end}}</td>
<td>{{ts .LastRequest}}<div class="mut">{{ago .LastRequest}}</div></td>
<td>{{ts .LastNodeAt}}<div class="mut mono">{{.LastNode}}</div></td>
<td>{{ts .LastPanic}}</td>
<td>{{.Counters.Requests}} / {{.Counters.Grants}} / {{.Counters.Denies}}</td>
<td>{{ts .Counters.LastRequest}}</td></tr>{{end}}
</table></div>{{template "foot"}}{{end}}

{{define "hostTable"}}
<div class="tw"><table><tr><th>Host</th><th>Cluster</th><th>State</th><th>Goahead given</th><th>Waiting for</th><th>Panic threshold</th><th>Last panic fired</th><th>Check attempts</th><th>Last check</th><th>Host seen</th><th>Forget</th></tr>
{{range .}}<tr>
<td class="mono">{{.Fqdn}}</td>
<td><a href="/ui/cluster/{{.Cluster}}">{{.Cluster}}</a>{{if .ConfigURL}}<div><a href="{{.ConfigURL}}">config</a></div>{{end}}</td>
<td>{{template "statuspill" .Class}}{{if eq .Class "stuck"}}<div class="mut">no panic threshold configured</div>{{end}}{{if not .GoneSince.IsZero}}<div class="mut">seen as removed since {{ts .GoneSince}}</div>{{end}}</td>
<td>{{ts .GoaheadAt}}</td>
<td><b>{{dur .Elapsed}}</b></td>
<td>{{if .Threshold}}{{dur .Threshold}}{{else}}not set{{end}}</td>
<td>{{ts .LastPanic}}<div class="mut">{{ago .LastPanic}}</div></td>
<td>{{with .Check}}{{template "statuspill" .State}}<div>{{.Attempts}} attempts, {{.Successes}} of {{.Required}} consecutive successes</div>{{if gt .Loops 1}}<div style="color:var(--bad)">{{.Loops}} check loops are running for this host</div>{{end}}{{else}}<span class="pill stuck">no check running</span><div class="mut">no completion check is polling this host</div>{{end}}</td>
<td>{{with .Check}}{{if not .LastRun.IsZero}}exit {{.LastExit}}, {{ts .LastRun}} ({{ago .LastRun}})<pre>{{.LastOutput}}</pre>{{else}}<span class="mut">not run yet{{if not .SleepUntil.IsZero}}, starts {{until .SleepUntil}}{{end}}</span>{{end}}{{end}}</td>
<td>{{ts .Seen.At}}<div class="mut">{{.Hint}}</div></td>
<td><code>{{.Forget}}</code></td></tr>{{end}}
</table></div>{{end}}

{{define "panic"}}{{template "head" .}}
<h1>Panicked hosts</h1>
<div class="note">A panic fires when a host got the goahead to restart and its reboot completion check did not pass within <code>reboot_completion_panic_threshold</code>. The panic actions and the mail are repeated once per threshold. Clusters without a threshold never panic, their hosts are listed as stuck after 3h. This page is read-only: forgetting a host is done on the server with the command shown.</div>
<h2>Did not come back in time ({{len (hostsByClass .Snap.Hosts "panic")}})</h2>
{{with hostsByClass .Snap.Hosts "panic"}}{{template "hostTable" .}}{{else}}<div class="tw none">No panicked hosts.</div>{{end}}
{{with hostsByClass .Snap.Hosts "stuck"}}<h2>Stuck, but the cluster has no panic threshold ({{len .}})</h2>{{template "hostTable" .}}{{end}}
{{with hostsByClass .Snap.Hosts "waiting"}}<h2>Restarting, still within the threshold ({{len .}})</h2>{{template "hostTable" .}}{{end}}
{{template "foot"}}{{end}}

{{define "activity"}}{{template "head" .}}
<h1>General activity</h1>
<h2>Hosts being checked ({{len .Snap.Checks}})</h2>
{{if .Snap.Checks}}<div class="tw"><table><tr><th>Host</th><th>Cluster</th><th>State</th><th>Check attempts</th><th>Consecutive successes</th><th>Last result</th><th>Started</th><th>Command</th></tr>
{{range .Snap.Checks}}<tr><td class="mono">{{.Fqdn}}</td><td><a href="/ui/cluster/{{.Cluster}}">{{.Cluster}}</a></td>
<td>{{template "statuspill" .State}}{{if not .SleepUntil.IsZero}}<div class="mut">starts {{until .SleepUntil}}</div>{{end}}{{if not .Finished.IsZero}}<div class="mut">{{ago .Finished}}</div>{{end}}</td>
<td><b>{{.Attempts}}</b>{{if gt .Loops 1}}<div style="color:var(--bad)">{{.Loops}} loops!</div>{{end}}</td><td>{{.Successes}} of {{.Required}}</td>
<td>{{if not .LastRun.IsZero}}exit {{.LastExit}} {{ago .LastRun}}<pre>{{.LastOutput}}</pre>{{end}}</td><td>{{ts .Started}}</td><td class="mono">{{.Command}}</td></tr>{{end}}
</table></div>{{else}}<div class="tw none">No completion check was started since goahead started at {{ts .Snap.Start}}.</div>{{end}}
<h2>Clusters with requests since start</h2>
<div class="tw"><table><tr><th>Cluster</th><th>Inquiries</th><th>Restart requests</th><th>Granted</th><th>Denied</th><th>Last request</th></tr>
{{range .Snap.Clusters}}{{if .Counters.LastRequest.IsZero}}{{else}}<tr><td><a href="/ui/cluster/{{.Name}}">{{.Name}}</a></td><td>{{.Counters.Inquires}}</td><td>{{.Counters.Requests}}</td><td>{{.Counters.Grants}}</td><td>{{.Counters.Denies}}</td><td>{{ts .Counters.LastRequest}} <span class="mut">{{ago .Counters.LastRequest}}</span></td></tr>{{end}}{{end}}
</table></div>
<h2>Recent events (newest first, last {{len .Snap.Events}})</h2>
{{template "events" .Snap.Events}}
{{template "foot"}}{{end}}

{{define "events"}}{{if .}}<div class="tw"><table><tr><th>Time</th><th>Cluster</th><th>Host</th><th>Event</th><th>Details</th></tr>
{{range .}}<tr><td>{{ts .Time}}</td><td><a href="/ui/cluster/{{.Cluster}}">{{.Cluster}}</a></td><td class="mono">{{.Fqdn}}</td><td>{{.Kind}}</td><td>{{.Message}}</td></tr>{{end}}</table></div>
{{else}}<div class="tw none">No events yet.</div>{{end}}{{end}}

{{define "cluster"}}{{template "head" .}}
<h1>{{.Cluster.Name}} {{template "statuspill" .Cluster.Status}}</h1>
{{if .Cluster.StatusNote}}<div class="note">{{.Cluster.StatusNote}}</div>{{end}}
<div class="cards">
<div class="card"><div class="n">{{.Cluster.Ongoing}} / {{.Cluster.Parallel}}</div><div class="l">restarting / allowed in parallel</div></div>
<div class="card"><div class="n" style="font-size:15px">{{ts .Cluster.LastRequest}}</div><div class="l">last restart given {{ago .Cluster.LastRequest}}</div></div>
<div class="card"><div class="n" style="font-size:15px">{{ts .Cluster.LastNodeAt}}</div><div class="l">last completed restart <span class="mono">{{.Cluster.LastNode}}</span></div></div>
<div class="card"><div class="n" style="font-size:15px">{{ts .Cluster.LastSuccess}}</div><div class="l">last slot freed (state file)</div></div>
<div class="card"><div class="n" style="font-size:15px">{{ts .Cluster.LastPanic}}</div><div class="l">last panic</div></div>
</div>
<h2>Settings</h2>
<div class="tw"><table>
<tr><th>config file</th><td class="mono">{{.Cluster.ConfigFile}}{{if .Cluster.ConfigURL}} · <a href="{{.Cluster.ConfigURL}}">open</a>{{end}}</td></tr>
<tr><th>name_pattern</th><td class="mono">{{.Setting.NamePattern}}</td></tr>
<tr><th>minimum_uptime</th><td>{{dur .Setting.MinimumUptime}}</td></tr>
<tr><th>reboot_completion_check</th><td class="mono">{{.Setting.RebootCompletionCheck}}</td></tr>
<tr><th>check interval / consecutive successes / offset</th><td>{{dur .Setting.RebootCompletionCheckInterval}} / {{.Setting.RebootCompletionCheckConsecutiveSuccesses}} / {{dur .Setting.RebootCompletionCheckOffset}}</td></tr>
<tr><th>panic threshold</th><td>{{if .Setting.RebootCompletionPanicThreshold}}{{dur .Setting.RebootCompletionPanicThreshold}}{{else}}not set{{end}}</td></tr>
<tr><th>panic mail</th><td>{{hosts .Setting.RebootCompletionPanicActions.Mail}}</td></tr>
</table></div>
<h2>Hosts being waited for</h2>
{{if .Hosts}}{{template "hostTable" .Hosts}}{{else}}<div class="tw none">No host is restarting.</div>{{end}}
{{if .Checks}}<h2>Completion checks</h2><div class="tw"><table><tr><th>Host</th><th>State</th><th>Attempts</th><th>Successes</th><th>Last result</th></tr>
{{range .Checks}}<tr><td class="mono">{{.Fqdn}}</td><td>{{template "statuspill" .State}}</td><td>{{.Attempts}}</td><td>{{.Successes}} of {{.Required}}</td><td>{{if not .LastRun.IsZero}}exit {{.LastExit}} {{ago .LastRun}}<pre>{{.LastOutput}}</pre>{{end}}</td></tr>{{end}}</table></div>{{end}}
<h2>Known hosts ({{len .Acks}}, from the ACK files)</h2>
<div class="tw"><table><tr><th>Host</th><th>Last reported uptime</th><th>ACK file written</th><th>Goahead flag</th><th>Message</th></tr>
{{range .Acks}}<tr><td class="mono">{{.RequestingFqdn}}</td><td>{{.ReportedUptime}}</td><td>{{ts .Timestamp}}</td><td>{{.Goahead}}</td><td>{{.Message}}</td></tr>{{end}}</table></div>
<h2>Recent events</h2>{{template "events" .Events}}
<h2>Cluster log (last {{len .LogLines}} lines)</h2>
<div class="log"><pre>{{range .LogLines}}{{.}}
{{end}}</pre></div>
{{template "foot"}}{{end}}
`))
