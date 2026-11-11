package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestRunRemovedHostCheckExitCodes(t *testing.T) {
	for code, want := range map[int]hostPresence{0: hostGone, 1: hostPresent, 2: hostUnknown, 127: hostUnknown} {
		r := runRemovedHostCheck(fmt.Sprintf("sh -c 'echo {:%%fqdn%%:}; exit %d'", code), "h.example.com", "c")
		if r.Presence != want || !strings.Contains(r.Detail, "h.example.com") {
			t.Errorf("exit %d: got %+v, want %v", code, r, want)
		}
	}
	if r := runRemovedHostCheck("/nonexistent/command", "h", "c"); r.Presence != hostUnknown {
		t.Errorf("missing command must be unknown: %+v", r)
	}
}

func TestHostRemovedCombination(t *testing.T) {
	oldH, oldD, oldS := hookLookup, dnsLookup, clusterSettings
	defer func() { hookLookup, dnsLookup, clusterSettings = oldH, oldD, oldS }()
	dns := hostUnknown
	hook := hostUnknown
	dnsLookup = func(string) presenceResult { return presenceResult{dns, "dns"} }
	hookLookup = func(string, string, string) presenceResult { return presenceResult{hook, "hook"} }

	clusterSettings = map[string]clusterSetting{"c": {RemovedHostCheck: "check {:%fqdn%:}"}, "nohook": {}}
	dns, hook = hostGone, hostPresent
	if hostRemoved("c", "a.b").Presence != hostPresent {
		t.Error("check says the host exists: NXDOMAIN alone must not remove it")
	}
	dns, hook = hostPresent, hostGone
	if hostRemoved("c", "a.b").Presence != hostGone {
		t.Error("check says removed: removed, even if DNS still resolves")
	}
	dns, hook = hostGone, hostUnknown
	if hostRemoved("c", "a.b").Presence != hostGone {
		t.Error("check can not decide: fall back to DNS NXDOMAIN")
	}
	dns, hook = hostUnknown, hostUnknown
	if hostRemoved("c", "a.b").Presence != hostUnknown {
		t.Error("nothing definite: unknown")
	}
	dns, hook = hostGone, hostPresent
	if hostRemoved("nohook", "a.b").Presence != hostGone {
		t.Error("no check configured: only DNS counts")
	}
}

func setupRemovedTest(t *testing.T) (dir string, mailOut string) {
	dir = t.TempDir()
	mailOut = filepath.Join(dir, "mail.out")
	fakeSendmail := filepath.Join(dir, "sendmail")
	if err := os.WriteFile(fakeSendmail, []byte("#!/bin/sh\ncat > "+mailOut+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	config = configSettings{LogBaseDir: dir + "/", SaveStateDir: dir + "/", SendmailPath: fakeSendmail}
	clusterLoggers = map[string]*logrus.Entry{"c": initLogger("c")}
	sleepingClusterChecks = map[string]clusterCheck{}
	goneSince = map[string]time.Time{}
	return dir, mailOut
}

func TestReleaseRemovedHost(t *testing.T) {
	dir, mailOut := setupRemovedTest(t)
	oldD := dnsLookup
	defer func() { dnsLookup = oldD }()
	dnsLookup = func(fqdn string) presenceResult {
		if fqdn == "gone.d" {
			return presenceResult{hostGone, "DNS NXDOMAIN"}
		}
		return presenceResult{hostPresent, "ok"}
	}
	clusterSettings = map[string]clusterSetting{"c": {Enabled: true, ReleaseRemovedHosts: true, RemovedHostGrace: time.Hour,
		RebootCompletionPanicActions: rebootCompletionPanicActionsStruct{Mail: []string{"a@example.com"}}}}
	cs := clusterState{CurrentOngoingRestarts: 2, CurrentRestartingServers: map[string]struct{}{"gone.d": {}, "alive.d": {}}}
	if err := writeStructJSONFile(filepath.Join(dir, "c.json"), cs); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "c"), 0755)
	os.WriteFile(filepath.Join(dir, "c", "gone.d.json"), []byte("{}"), 0644)

	checkRemovedHosts("c") // first sighting starts the grace period
	if !hostStillRestarting("c", "gone.d", clusterLoggers["c"]) {
		t.Fatal("host must not be released before the grace period elapsed")
	}
	goneSince["c/gone.d"] = time.Now().Add(-2 * time.Hour)
	checkRemovedHosts("c")
	if hostStillRestarting("c", "gone.d", clusterLoggers["c"]) {
		t.Error("removed host must be released after the grace period")
	}
	if !hostStillRestarting("c", "alive.d", clusterLoggers["c"]) {
		t.Error("existing host must stay")
	}
	got := readClusterStateFile(filepath.Join(dir, "c.json"), "c", clusterLoggers["c"])
	if got.CurrentOngoingRestarts != 1 {
		t.Errorf("CurrentOngoingRestarts = %d, want 1", got.CurrentOngoingRestarts)
	}
	if fileExists(filepath.Join(dir, "c", "gone.d.json")) {
		t.Error("ACK file must be deleted")
	}
	if m, _ := os.ReadFile(mailOut); len(m) == 0 {
		t.Error("expected a mail about the release")
	}
}

func TestRemovedHostSeenAgainResetsGrace(t *testing.T) {
	dir, _ := setupRemovedTest(t)
	oldD := dnsLookup
	defer func() { dnsLookup = oldD }()
	state := hostGone
	dnsLookup = func(string) presenceResult { return presenceResult{state, "x"} }
	clusterSettings = map[string]clusterSetting{"c": {Enabled: true, ReleaseRemovedHosts: true}}
	cs := clusterState{CurrentOngoingRestarts: 1, CurrentRestartingServers: map[string]struct{}{"h.d": {}}}
	writeStructJSONFile(filepath.Join(dir, "c.json"), cs)
	checkRemovedHosts("c")
	if _, ok := goneSince["c/h.d"]; !ok {
		t.Fatal("expected grace period to start")
	}
	state = hostPresent
	checkRemovedHosts("c")
	if _, ok := goneSince["c/h.d"]; ok {
		t.Error("a host seen again must reset the grace period")
	}
}

func TestForgetHost(t *testing.T) {
	dir, _ := setupRemovedTest(t)
	cs := clusterState{CurrentOngoingRestarts: 1, CurrentRestartingServers: map[string]struct{}{"h.d": {}}}
	writeStructJSONFile(filepath.Join(dir, "c.json"), cs)
	if !forgetHost("h.d", "", clusterLoggers["c"]) {
		t.Fatal("host must be found")
	}
	if hostStillRestarting("c", "h.d", clusterLoggers["c"]) {
		t.Error("host must be forgotten")
	}
	if forgetHost("h.d", "", clusterLoggers["c"]) {
		t.Error("second forget must report not found")
	}
}
