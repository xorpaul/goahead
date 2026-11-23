package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// setupUITest builds a state directory in the shape of the real files of the goahead server: a panicked cluster, a cluster without panic threshold, an idle cluster and a disabled one
func setupUITest(t *testing.T) *mux.Router {
	t.Helper()
	dir := t.TempDir()
	resetUI()
	config = configSettings{SaveStateDir: dir + "/", LogBaseDir: dir + "/", IncludeDir: "conf.d", ClusterConfigURLBase: "https://git.example/files/cluster.conf.d/"}
	clusterSettings = map[string]clusterSetting{
		"es":    {Enabled: true, AllowedParallelRestarts: 1, RebootCompletionPanicThreshold: 3 * time.Hour, RebootCompletionCheck: "/bin/false", RebootCompletionCheckConsecutiveSuccesses: 15, ConfigFile: "conf.d/live/itohi/es.yml"},
		"nopan": {Enabled: true, AllowedParallelRestarts: 1, ConfigFile: "conf.d/live/itohi/nopan.yml"},
		"quiet": {Enabled: true, AllowedParallelRestarts: 1, RebootCompletionPanicThreshold: time.Hour},
		"off":   {Enabled: false, AllowedParallelRestarts: 1, RebootCompletionPanicThreshold: time.Hour, RebootCompletionCheckOffset: 2 * time.Hour},
	}
	clusterLoggers = map[string]*logrus.Entry{}
	for n := range clusterSettings {
		clusterLoggers[n] = logrus.NewEntry(logrus.New())
	}
	mainLogger = logrus.NewEntry(logrus.New())

	write := func(name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ago := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339Nano) }
	write("es.json", `{"last_restart_panic_timestamp":"`+ago(time.Minute)+`","last_restart_request_timestamp":"`+ago(5*time.Hour)+`","last_successful_restart_timestamp":"0001-01-01T00:00:00Z","CurrentOngoingRestarts":1,"CurrentRestartingServers":{"esearch-vault-live-bap03.server.lan":{}}}`)
	write("nopan.json", `{"last_restart_panic_timestamp":"0001-01-01T00:00:00Z","last_restart_request_timestamp":"`+ago(4*time.Hour)+`","last_successful_restart_timestamp":"0001-01-01T00:00:00Z","CurrentOngoingRestarts":1,"CurrentRestartingServers":{"zeus-pre-fe1.server.lan":{}}}`)
	write("quiet.json", `{"last_restart_panic_timestamp":"0001-01-01T00:00:00Z","last_restart_request_timestamp":"`+ago(2*time.Hour)+`","last_successful_restart_timestamp":"`+ago(time.Hour)+`","CurrentOngoingRestarts":0,"CurrentRestartingServers":{}}`)
	write("quiet/h1.server.lan.json", `{"timestamp":"`+ago(time.Hour)+`","go_ahead":false,"unknown_host":false,"request_id":"x","found_cluster":"quiet","requesting_fqdn":"h1.server.lan","message":"fqdn: h1.server.lan seems to have successfully rebooted in cluster quiet at now","reported_uptime":"10m"}`)
	write("es.log", strings.Repeat("old line\n", 10)+"last line <script>alert(1)</script>\n")

	r := mux.NewRouter()
	addUIRoutes(r)
	return r
}

func get(t *testing.T, r http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func TestUIPanicPage(t *testing.T) {
	r := setupUITest(t)
	cc := clusterCheck{clusterSettings["es"], "esearch-vault-live-bap03.server.lan", "rid", "es"}
	uiCheckStart(cc)
	for i := 0; i < 3; i++ {
		uiCheckAttempt(cc, 1, "WARNING <b>HTTP</b> 301", 0)
	}
	uiNoteRequest("es", cc.Fqdn, "3h10m0s", "inquire", false)

	code, body := get(t, r, "/ui/panic")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{"esearch-vault-live-bap03.server.lan", "Did not come back in time (1)", "3 attempts, 0 of 15 consecutive successes", "exit 1", "host reports again (uptime 3h10m0s)",
		"-forget esearch-vault-live-bap03.server.lan -cluster es", "zeus-pre-fe1.server.lan", "no panic threshold configured", "https://git.example/files/cluster.conf.d/live/itohi/es.yml", "no check running"} {
		if !strings.Contains(body, want) {
			t.Errorf("panic page does not contain %q", want)
		}
	}
	if strings.Contains(body, "<b>HTTP</b>") {
		t.Error("check output is not escaped")
	}
	if !strings.Contains(body, "&lt;b&gt;HTTP&lt;/b&gt; 301") {
		t.Error("escaped check output missing")
	}
}

func TestUIOverviewOrderAndLastNode(t *testing.T) {
	r := setupUITest(t)
	code, body := get(t, r, "/ui/")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// problems first: panic, stuck, idle, disabled
	order := []string{"/ui/cluster/es\"", "/ui/cluster/nopan\"", "/ui/cluster/quiet\"", "/ui/cluster/off\""}
	last := -1
	for _, o := range order {
		i := strings.Index(body, o)
		if i < 0 || i < last {
			t.Fatalf("cluster %s missing or out of order in overview", o)
		}
		last = i
	}
	if !strings.Contains(body, "h1.server.lan") {
		t.Error("last completed restart node of cluster quiet missing")
	}
	if !strings.Contains(body, "automatically disabled") {
		t.Error("auto disabled note missing")
	}
}

func TestUIClusterPageAndUnknownCluster(t *testing.T) {
	r := setupUITest(t)
	code, body := get(t, r, "/ui/cluster/es")
	if code != 200 || !strings.Contains(body, "last line &lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("cluster page status %d or log tail missing/unescaped", code)
	}
	if code, _ := get(t, r, "/ui/cluster/doesnotexist"); code != 404 {
		t.Errorf("unknown cluster: status %d, want 404", code)
	}
	// mux cleans the path and redirects, the handler is never reached with such a name
	if code, body := get(t, r, "/ui/cluster/..%2f..%2fetc%2fpasswd"); code == 200 || strings.Contains(body, "root:") {
		t.Errorf("path traversal attempt: status %d", code)
	}
}

func TestUIActivityAndJSON(t *testing.T) {
	r := setupUITest(t)
	cc := clusterCheck{clusterSettings["es"], "h2.server.lan", "rid", "es"}
	uiCheckSleeping(cc, time.Now().Add(15*time.Minute))
	uiNoteRequest("es", "h2.server.lan", "1h", "request", true)
	uiRecordEvent("es", "h2.server.lan", "goahead", "restart granted")
	code, body := get(t, r, "/ui/activity")
	if code != 200 || !strings.Contains(body, "h2.server.lan") || !strings.Contains(body, "sleeping") || !strings.Contains(body, "restart granted") {
		t.Errorf("activity page status %d, content missing", code)
	}
	code, body = get(t, r, "/ui/status.json")
	var snap snapshot
	if code != 200 || json.Unmarshal([]byte(body), &snap) != nil || snap.NPanic != 1 || snap.NStuck != 1 {
		t.Errorf("status.json status %d panic %d stuck %d", code, snap.NPanic, snap.NStuck)
	}
}

func TestUIReadOnly(t *testing.T) {
	r := setupUITest(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/panic", nil))
	if rec.Code == 200 {
		t.Error("POST to the web interface must not be accepted")
	}
}

func TestUICheckLoopsAndAttempts(t *testing.T) {
	resetUI()
	cc := clusterCheck{clusterSetting{RebootCompletionCheckConsecutiveSuccesses: 2}, "h.lan", "rid", "c"}
	uiCheckStart(cc)
	uiCheckStart(cc) // a second loop for the same host
	uiCheckAttempt(cc, 0, "OK", 1)
	uiMu.Lock()
	cs := *uiChecks["c/h.lan"]
	uiMu.Unlock()
	if cs.Loops != 2 || cs.Attempts != 1 || cs.Required != 2 {
		t.Errorf("unexpected check status %+v", cs)
	}
	uiCheckEnd(cc, "stopped", "x")
	uiCheckEnd(cc, "completed", "y")
	uiMu.Lock()
	state := uiChecks["c/h.lan"].State
	uiMu.Unlock()
	if state != "completed" {
		t.Errorf("state %q, want completed after both loops ended", state)
	}
}

func TestTailLogBounded(t *testing.T) {
	dir := t.TempDir()
	config = configSettings{LogBaseDir: dir + "/"}
	big := strings.Repeat("x", 700) + "\n"
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString(big)
	}
	b.WriteString("final\n")
	if err := os.WriteFile(filepath.Join(dir, "c.log"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	lines := tailLog("c")
	if len(lines) > uiLogTailLines || lines[len(lines)-1] != "final" || !strings.HasSuffix(lines[0], "[line truncated]") {
		t.Errorf("unexpected tail: %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestUISleepingKeepsRunningLoop(t *testing.T) {
	resetUI()
	cc := clusterCheck{clusterSetting{RebootCompletionCheckConsecutiveSuccesses: 3}, "h.lan", "rid", "c"}
	uiCheckStart(cc)
	uiCheckAttempt(cc, 1, "WARNING", 0)
	uiCheckAttempt(cc, 1, "WARNING", 0)
	uiCheckSleeping(cc, time.Now().Add(time.Minute)) // the host asked again for a restart while its loop polls
	uiCheckStart(cc)                                 // and the second loop starts
	uiMu.Lock()
	cs := *uiChecks["c/h.lan"]
	uiMu.Unlock()
	if cs.Loops != 2 || cs.Attempts != 2 || cs.State != "polling" {
		t.Errorf("want 2 loops, 2 attempts kept, polling; got %+v", cs)
	}
}
