package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// The web interface is strictly read-only. It is built from the cluster state files, the ACK files and a few in-memory registries that are filled while goahead runs (check attempts, last seen hosts, recent events). It never parses the (huge) log files, only the tail of one cluster log on the cluster page.

const (
	uiMaxEvents = 500
	// uiOutputMax caps the stored output of one completion check run
	uiOutputMax = 2000
	// uiStuckAfterNoPanic marks hosts of clusters without reboot_completion_panic_threshold as stuck, because nothing else would ever report them
	uiStuckAfterNoPanic = 3 * time.Hour
	uiLogTailBytes      = 128 * 1024
	uiLogTailLines      = 60
	uiLogLineMax        = 600
	uiCompletedKeep     = 24 * time.Hour
	uiAckCacheTTL       = 20 * time.Second
	uiMaxAckFiles       = 1000
)

// uiEvent is one entry of the activity feed
type uiEvent struct {
	Time    time.Time
	Cluster string
	Fqdn    string
	Kind    string
	Message string
}

// checkStatus is the state of the reboot completion check of one host
type checkStatus struct {
	Cluster    string
	Fqdn       string
	Command    string
	State      string // sleeping, polling, completed, stopped
	Attempts   int
	Successes  int // current consecutive successes
	Required   int
	Loops      int // number of check loops running for this host, more than 1 is a bug
	LastExit   int
	LastOutput string
	Started    time.Time
	LastRun    time.Time
	SleepUntil time.Time
	Finished   time.Time
}

type hostSeenInfo struct {
	At     time.Time
	Uptime string
}

type clusterCounters struct {
	Requests    int
	Inquires    int
	Grants      int
	Denies      int
	LastRequest time.Time
}

var (
	uiMu        sync.Mutex
	uiChecks    = map[string]*checkStatus{}
	uiSeen      = map[string]hostSeenInfo{}
	uiCounters  = map[string]*clusterCounters{}
	uiEvents    []uiEvent
	serverStart = time.Now()

	uiAckMu    sync.Mutex
	uiAckCache = map[string]uiAckCacheEntry{}
)

type uiAckCacheEntry struct {
	At   time.Time
	Acks []response
}

func uiKey(cluster, fqdn string) string { return cluster + "/" + fqdn }

// resetUI clears all in-memory registries, used by the tests
func resetUI() {
	uiMu.Lock()
	uiChecks = map[string]*checkStatus{}
	uiSeen = map[string]hostSeenInfo{}
	uiCounters = map[string]*clusterCounters{}
	uiEvents = nil
	uiMu.Unlock()
	uiAckMu.Lock()
	uiAckCache = map[string]uiAckCacheEntry{}
	uiAckMu.Unlock()
}

func uiRecordEvent(cluster, fqdn, kind, msg string) {
	uiMu.Lock()
	defer uiMu.Unlock()
	uiEvents = append(uiEvents, uiEvent{Time: time.Now(), Cluster: cluster, Fqdn: fqdn, Kind: kind, Message: msg})
	if len(uiEvents) > uiMaxEvents {
		uiEvents = append([]uiEvent(nil), uiEvents[len(uiEvents)-uiMaxEvents:]...)
	}
}

// uiNoteRequest counts a request of a host that matched a cluster
func uiNoteRequest(cluster, fqdn, uptime, kind string, granted bool) {
	uiMu.Lock()
	defer uiMu.Unlock()
	uiSeen[uiKey(cluster, fqdn)] = hostSeenInfo{At: time.Now(), Uptime: uptime}
	c := uiCounters[cluster]
	if c == nil {
		c = &clusterCounters{}
		uiCounters[cluster] = c
	}
	c.LastRequest = time.Now()
	switch {
	case kind == "inquire":
		c.Inquires++
	case granted:
		c.Requests++
		c.Grants++
	default:
		c.Requests++
		c.Denies++
	}
}

func uiCheckSleeping(cc clusterCheck, until time.Time) {
	uiMu.Lock()
	if cs := uiChecks[uiKey(cc.Cluster, cc.Fqdn)]; cs != nil && cs.Loops > 0 {
		// a restart request of a host that is already being polled schedules a second loop: keep the running loops and their attempts, the Loops counter shows it
		cs.SleepUntil = until
	} else {
		uiChecks[uiKey(cc.Cluster, cc.Fqdn)] = &checkStatus{Cluster: cc.Cluster, Fqdn: cc.Fqdn, Command: cc.Csetting.RebootCompletionCheck, State: "sleeping",
			Required: cc.Csetting.RebootCompletionCheckConsecutiveSuccesses, SleepUntil: until}
	}
	uiMu.Unlock()
	uiRecordEvent(cc.Cluster, cc.Fqdn, "check-scheduled", "reboot completion check starts after the offset "+cc.Csetting.RebootCompletionCheckOffset.String())
}

func uiCheckStart(cc clusterCheck) {
	uiMu.Lock()
	cs := uiChecks[uiKey(cc.Cluster, cc.Fqdn)]
	if cs == nil || cs.State == "completed" || cs.State == "stopped" {
		cs = &checkStatus{Cluster: cc.Cluster, Fqdn: cc.Fqdn}
		uiChecks[uiKey(cc.Cluster, cc.Fqdn)] = cs
	}
	if cs.Loops == 0 {
		// a new run: attempts count from here, a second parallel loop must not reset them
		cs.Attempts, cs.Successes = 0, 0
		cs.Started = time.Now()
	}
	cs.Loops++
	cs.State = "polling"
	cs.Command = cc.Csetting.RebootCompletionCheck
	cs.Required = cc.Csetting.RebootCompletionCheckConsecutiveSuccesses
	cs.SleepUntil = time.Time{}
	uiMu.Unlock()
	uiRecordEvent(cc.Cluster, cc.Fqdn, "check-started", "polling the reboot completion check every "+cc.Csetting.RebootCompletionCheckInterval.String())
}

func uiCheckAttempt(cc clusterCheck, exit int, output string, successes int) {
	if len(output) > uiOutputMax {
		output = output[:uiOutputMax] + " [truncated]"
	}
	uiMu.Lock()
	defer uiMu.Unlock()
	if cs := uiChecks[uiKey(cc.Cluster, cc.Fqdn)]; cs != nil {
		cs.Attempts++
		cs.LastExit = exit
		cs.LastOutput = strings.TrimSpace(output)
		cs.LastRun = time.Now()
		cs.Successes = successes
	}
}

func uiCheckEnd(cc clusterCheck, state, msg string) {
	uiMu.Lock()
	if cs := uiChecks[uiKey(cc.Cluster, cc.Fqdn)]; cs != nil {
		cs.Loops--
		if cs.Loops <= 0 {
			cs.Loops = 0
			cs.State = state
			cs.Finished = time.Now()
		}
	}
	uiMu.Unlock()
	uiRecordEvent(cc.Cluster, cc.Fqdn, "check-"+state, msg)
}

// ---------------------------------------------------------------- view model

type hostRow struct {
	Cluster   string
	Fqdn      string
	Class     string // panic, stuck, waiting
	GoaheadAt time.Time
	Elapsed   time.Duration
	Threshold time.Duration
	LastPanic time.Time
	Check     *checkStatus
	Seen      hostSeenInfo
	Hint      string
	GoneSince time.Time
	Forget    string
	ConfigURL string
}

type clusterRow struct {
	Name        string
	Status      string // panic, stuck, restarting, idle, disabled, unreadable
	StatusNote  string
	Enabled     bool
	Parallel    int
	Ongoing     int
	Restarting  []string
	LastRequest time.Time
	LastSuccess time.Time
	LastPanic   time.Time
	LastNode    string
	LastNodeAt  time.Time
	Counters    clusterCounters
	Threshold   time.Duration
	ConfigFile  string
	ConfigURL   string
}

type snapshot struct {
	Generated time.Time
	Version   string
	BuildTime string
	Start     time.Time
	Uptime    time.Duration
	Clusters  []clusterRow
	Hosts     []hostRow // every host that is in a CurrentRestartingServers
	Checks    []checkStatus
	Events    []uiEvent
	NPanic    int
	NStuck    int
	NRestart  int
	NDisabled int
}

var statusOrder = map[string]int{"panic": 0, "stuck": 1, "unreadable": 2, "restarting": 3, "waiting": 3, "idle": 4, "disabled": 5}

// readClusterStateQuiet reads a cluster state file under the global mutex, because the writers hold it as well
func readClusterStateQuiet(cluster string) (cs clusterState, exists bool, err error) {
	file := filepath.Join(config.SaveStateDir, cluster+".json")
	mutex.Lock()
	defer mutex.Unlock()
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return cs, false, nil
		}
		return cs, false, rerr
	}
	if len(data) == 0 {
		return cs, true, nil
	}
	return cs, true, json.Unmarshal(data, &cs)
}

// readAcks returns the ACK files of a cluster, cached for a few seconds because the overview asks for every cluster
func readAcks(cluster string) []response {
	uiAckMu.Lock()
	if e, ok := uiAckCache[cluster]; ok && time.Since(e.At) < uiAckCacheTTL {
		uiAckMu.Unlock()
		return e.Acks
	}
	uiAckMu.Unlock()
	var acks []response
	entries, _ := os.ReadDir(filepath.Join(config.SaveStateDir, cluster))
	for i, e := range entries {
		if i >= uiMaxAckFiles {
			break
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(config.SaveStateDir, cluster, e.Name()))
		if err != nil {
			continue
		}
		var r response
		if json.Unmarshal(data, &r) == nil {
			if r.RequestingFqdn == "" {
				r.RequestingFqdn = strings.TrimSuffix(e.Name(), ".json")
			}
			acks = append(acks, r)
		}
	}
	sort.Slice(acks, func(i, j int) bool { return acks[i].RequestingFqdn < acks[j].RequestingFqdn })
	uiAckMu.Lock()
	uiAckCache[cluster] = uiAckCacheEntry{At: time.Now(), Acks: acks}
	uiAckMu.Unlock()
	return acks
}

// lastCompleted finds the host with the newest "successfully rebooted" ACK file of a cluster
func lastCompleted(acks []response) (string, time.Time) {
	var fqdn string
	var at time.Time
	for _, a := range acks {
		if strings.Contains(a.Message, "successfully rebooted") && a.Timestamp.After(at) {
			fqdn, at = a.RequestingFqdn, a.Timestamp
		}
	}
	return fqdn, at
}

func classifyHost(threshold time.Duration, elapsed time.Duration) string {
	switch {
	case threshold > 0 && elapsed > threshold:
		return "panic"
	case threshold <= 0 && elapsed > uiStuckAfterNoPanic:
		return "stuck"
	}
	return "waiting"
}

func buildSnapshot() snapshot {
	now := time.Now()
	snap := snapshot{Generated: now, Version: buildversion, BuildTime: buildtime, Start: serverStart, Uptime: now.Sub(serverStart)}
	if snap.Version == "" {
		snap.Version = "dev"
	}

	// copy the registries first, the state files are read without holding uiMu
	uiMu.Lock()
	checks := make(map[string]checkStatus, len(uiChecks))
	for k, v := range uiChecks {
		checks[k] = *v
	}
	seen := make(map[string]hostSeenInfo, len(uiSeen))
	for k, v := range uiSeen {
		seen[k] = v
	}
	counters := make(map[string]clusterCounters, len(uiCounters))
	for k, v := range uiCounters {
		counters[k] = *v
	}
	snap.Events = make([]uiEvent, 0, len(uiEvents))
	for i := len(uiEvents) - 1; i >= 0; i-- { // newest first
		snap.Events = append(snap.Events, uiEvents[i])
	}
	uiMu.Unlock()

	goneSinceMu.Lock()
	gone := make(map[string]time.Time, len(goneSince))
	for k, v := range goneSince {
		gone[k] = v
	}
	goneSinceMu.Unlock()

	names := make([]string, 0, len(clusterSettings))
	for n := range clusterSettings {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		set := clusterSettings[name]
		row := clusterRow{Name: name, Enabled: set.Enabled, Parallel: set.AllowedParallelRestarts, Threshold: set.RebootCompletionPanicThreshold,
			ConfigFile: set.ConfigFile, ConfigURL: clusterConfigURL(set), Counters: counters[name], Status: "idle"}
		cs, exists, err := readClusterStateQuiet(name)
		if err != nil {
			row.Status, row.StatusNote = "unreadable", "state file: "+err.Error()
		}
		if exists {
			row.Ongoing = cs.CurrentOngoingRestarts
			row.LastRequest = cs.LastRestartRequestTimestamp
			row.LastSuccess = cs.LastSuccessfulRestartTimestamp
			row.LastPanic = cs.LastRestartPanicTimestamp
			row.Restarting = keysString(cs.CurrentRestartingServers)
			sort.Strings(row.Restarting)
		}
		row.LastNode, row.LastNodeAt = lastCompleted(readAcks(name))

		worst := ""
		for _, fqdn := range row.Restarting {
			h := hostRow{Cluster: name, Fqdn: fqdn, GoaheadAt: row.LastRequest, Threshold: set.RebootCompletionPanicThreshold, LastPanic: row.LastPanic,
				Seen: seen[uiKey(name, fqdn)], GoneSince: gone[uiKey(name, fqdn)], Forget: forgetCommand(fqdn, name), ConfigURL: row.ConfigURL}
			if !row.LastRequest.IsZero() {
				h.Elapsed = now.Sub(row.LastRequest)
			}
			h.Class = classifyHost(h.Threshold, h.Elapsed)
			if c, ok := checks[uiKey(name, fqdn)]; ok {
				cc := c
				h.Check = &cc
			}
			switch {
			case h.Seen.At.IsZero():
				h.Hint = "no request of this host seen since goahead started at " + snap.Start.Format("2006-01-02 15:04:05")
			case h.Seen.At.After(h.GoaheadAt):
				h.Hint = "host reports again (uptime " + h.Seen.Uptime + "), so it is up: its completion check does not pass"
			default:
				h.Hint = "no request of this host since the goahead, it is silent"
			}
			snap.Hosts = append(snap.Hosts, h)
			if statusOrder[h.Class] < statusOrder[worst] || worst == "" {
				worst = h.Class
			}
		}
		if row.Status != "unreadable" {
			switch {
			case len(row.Restarting) > 0 && (worst == "panic" || worst == "stuck"):
				row.Status = worst
			case len(row.Restarting) > 0:
				row.Status = "restarting"
			}
			if !set.Enabled {
				row.Status = "disabled"
				if set.RebootCompletionPanicThreshold > 0 && set.RebootCompletionCheckOffset > set.RebootCompletionPanicThreshold {
					row.StatusNote = "automatically disabled: reboot_completion_check_offset is larger than reboot_completion_panic_threshold"
				}
			}
		}
		switch row.Status {
		case "panic":
			snap.NPanic++
		case "stuck":
			snap.NStuck++
		case "restarting":
			snap.NRestart++
		case "disabled":
			snap.NDisabled++
		}
		snap.Clusters = append(snap.Clusters, row)
	}
	sort.SliceStable(snap.Clusters, func(i, j int) bool {
		return statusOrder[snap.Clusters[i].Status] < statusOrder[snap.Clusters[j].Status]
	})
	sort.SliceStable(snap.Hosts, func(i, j int) bool {
		if statusOrder[snap.Hosts[i].Class] != statusOrder[snap.Hosts[j].Class] {
			return statusOrder[snap.Hosts[i].Class] < statusOrder[snap.Hosts[j].Class]
		}
		return snap.Hosts[i].Elapsed > snap.Hosts[j].Elapsed
	})

	for k, c := range checks {
		if (c.State == "completed" || c.State == "stopped") && now.Sub(c.Finished) > uiCompletedKeep {
			uiMu.Lock()
			if cur := uiChecks[k]; cur != nil && cur.Finished.Equal(c.Finished) {
				delete(uiChecks, k)
			}
			uiMu.Unlock()
			continue
		}
		snap.Checks = append(snap.Checks, c)
	}
	sort.Slice(snap.Checks, func(i, j int) bool {
		a, b := snap.Checks[i], snap.Checks[j]
		if (a.State == "polling" || a.State == "sleeping") != (b.State == "polling" || b.State == "sleeping") {
			return a.State == "polling" || a.State == "sleeping"
		}
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		return a.Fqdn < b.Fqdn
	})
	return snap
}

// tailLog returns the last lines of a cluster log without reading the whole file
func tailLog(cluster string) []string {
	f, err := os.Open(filepath.Join(config.LogBaseDir, cluster+".log"))
	if err != nil {
		return []string{"could not open log: " + err.Error()}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	offset := st.Size() - uiLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(f, uiLogTailBytes+1))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // first line is cut off
	}
	if len(lines) > uiLogTailLines {
		lines = lines[len(lines)-uiLogTailLines:]
	}
	for i, l := range lines {
		if len(l) > uiLogLineMax {
			lines[i] = l[:uiLogLineMax] + " ...[line truncated]"
		}
	}
	return lines
}

// ---------------------------------------------------------------- handlers

func addUIRoutes(r *mux.Router) {
	get := func(path string, h http.HandlerFunc) {
		r.HandleFunc(path, uiProtect(h)).Methods(http.MethodGet, http.MethodHead)
	}
	get("/ui", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/ui/", http.StatusMovedPermanently)
	})
	get("/ui/", uiOverviewHandler)
	get("/ui/panic", uiPanicHandler)
	get("/ui/activity", uiActivityHandler)
	get("/ui/cluster/{name}", uiClusterHandler)
	get("/ui/status.json", uiStatusJSONHandler)
}

func renderPage(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := uiTemplates.ExecuteTemplate(w, name, data); err != nil {
		mainLogger.Warn("rendering web interface page " + name + " failed: " + err.Error())
	}
}

type pageData struct {
	Page string
	Snap snapshot
}

func uiOverviewHandler(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "overview", pageData{"overview", buildSnapshot()})
}

func uiPanicHandler(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "panic", pageData{"panic", buildSnapshot()})
}

func uiActivityHandler(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "activity", pageData{"activity", buildSnapshot()})
}

func uiStatusJSONHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(buildSnapshot())
}

type clusterPage struct {
	Page     string
	Snap     snapshot
	Cluster  clusterRow
	Setting  clusterSetting
	Hosts    []hostRow
	Acks     []response
	Checks   []checkStatus
	Events   []uiEvent
	LogLines []string
}

func uiClusterHandler(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	set, ok := clusterSettings[name] // only configured clusters, so the name is safe to use in a path
	if !ok {
		http.NotFound(w, r)
		return
	}
	snap := buildSnapshot()
	cp := clusterPage{Page: "cluster", Snap: snap, Setting: set, Acks: readAcks(name), LogLines: tailLog(name)}
	for _, c := range snap.Clusters {
		if c.Name == name {
			cp.Cluster = c
		}
	}
	for _, h := range snap.Hosts {
		if h.Cluster == name {
			cp.Hosts = append(cp.Hosts, h)
		}
	}
	for _, c := range snap.Checks {
		if c.Cluster == name {
			cp.Checks = append(cp.Checks, c)
		}
	}
	for _, e := range snap.Events {
		if e.Cluster == name && len(cp.Events) < 100 {
			cp.Events = append(cp.Events, e)
		}
	}
	renderPage(w, "cluster", cp)
}
