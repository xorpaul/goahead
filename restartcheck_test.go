package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// After a restart of goahead the hosts in the cluster state file must get their reboot completion check again. checkCurrentClusterStates only put them into sleepingClusterChecks before, nothing ever started the check.
func TestCheckCurrentClusterStatesSchedulesChecks(t *testing.T) {
	dir := t.TempDir()
	resetUI()
	config = configSettings{SaveStateDir: dir + "/", LogBaseDir: dir + "/"}
	logger := logrus.NewEntry(logrus.New())
	mainLogger, checkerLogger = logger, logger
	clusterLoggers = map[string]*logrus.Entry{"c": logger}
	sleepingClusterChecks = map[string]clusterCheck{}
	clusterSettings = map[string]clusterSetting{"c": {Enabled: true, AllowedParallelRestarts: 1, RebootCompletionCheck: "/bin/true",
		RebootCompletionCheckConsecutiveSuccesses: 1, RebootCompletionCheckOffset: 50 * time.Millisecond}}
	if err := os.WriteFile(filepath.Join(dir, "c.json"), []byte(`{"CurrentOngoingRestarts":1,"CurrentRestartingServers":{"h.lan":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	checkCurrentClusterStates()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		uiMu.Lock()
		cs := uiChecks["c/h.lan"]
		done := cs != nil && cs.State == "completed"
		uiMu.Unlock()
		if done {
			cstate, _, _ := readClusterStateQuiet("c")
			if cstate.CurrentOngoingRestarts != 0 || len(cstate.CurrentRestartingServers) != 0 {
				t.Errorf("slot not freed: %+v", cstate)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the reboot completion check of the restarting host was never run after startup")
}
