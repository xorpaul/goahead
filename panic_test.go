package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
	"time"
)

func TestHandleRebootPanicMail(t *testing.T) {
	dir := t.TempDir()
	mailOut := filepath.Join(dir, "mail.out")
	fakeSendmail := filepath.Join(dir, "sendmail")
	if err := os.WriteFile(fakeSendmail, []byte("#!/bin/sh\necho \"ARGS: $@\" > "+mailOut+"\ncat >> "+mailOut+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	failCheck := filepath.Join(dir, "check.sh")
	if err := os.WriteFile(failCheck, []byte("#!/bin/sh\necho \"CRITICAL: $1 is down\"\nexit 2\n"), 0755); err != nil {
		t.Fatal(err)
	}

	config = configSettings{SaveStateDir: dir + "/", LogBaseDir: dir + "/", SendmailPath: fakeSendmail, MailFrom: "goahead@test.lan", IncludeDir: "conf.d", ClusterConfigURLBase: "https://git.example/src/branch/main/files/cluster.conf.d/"}
	clusterSettings = map[string]clusterSetting{"testcluster": {
		RebootCompletionCheck:          failCheck + " {:%fqdn%:}",
		RebootCompletionPanicThreshold: 3 * time.Hour,
		RebootCompletionPanicActions:   rebootCompletionPanicActionsStruct{Mail: []string{"ops@example.com"}, Scripts: []string{"/bin/echo panic for {:%hostname%:} in {:%cluster%:}"}},
		ConfigFile:                     "conf.d/live/itohi/dave.yml",
	}}
	clusterLoggers = map[string]*logrus.Entry{"testcluster": initLogger("panic-test")}

	handleRebootPanic(panicEvent{Cluster: "testcluster", Hosts: []string{"host1.example.com"}, Trigger: "timer", LastRestartRequest: time.Now().Add(-4 * time.Hour)}, clusterLoggers["testcluster"])

	data, err := os.ReadFile(mailOut)
	if err != nil {
		t.Fatalf("no mail sent: %v", err)
	}
	for _, want := range []string{"ARGS: -i -- ops@example.com", "Subject: [goahead] PANIC: host1.example.com", "Exit code: 2", "CRITICAL: host1.example.com is down", "panic for host1 in testcluster", "https://git.example/src/branch/main/files/cluster.conf.d/live/itohi/dave.yml"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("mail does not contain %q:\n%s", want, data)
		}
	}
}

func TestRebootPanicDue(t *testing.T) {
	clusterSettings = map[string]clusterSetting{"c": {RebootCompletionPanicThreshold: time.Hour}, "z": {}}
	restarting := map[string]struct{}{"h": {}}
	old := time.Now().Add(-2 * time.Hour)
	if !rebootPanicDue("c", clusterState{CurrentRestartingServers: restarting, LastRestartRequestTimestamp: old}) {
		t.Error("expected panic due")
	}
	if rebootPanicDue("c", clusterState{CurrentRestartingServers: restarting, LastRestartRequestTimestamp: time.Now()}) {
		t.Error("threshold not met")
	}
	if rebootPanicDue("c", clusterState{CurrentRestartingServers: restarting, LastRestartRequestTimestamp: old, LastRestartPanicTimestamp: time.Now()}) {
		t.Error("panic fired recently")
	}
	if rebootPanicDue("z", clusterState{CurrentRestartingServers: restarting, LastRestartRequestTimestamp: old}) {
		t.Error("threshold 0 must never panic")
	}
}

func TestPanicActionListFormat(t *testing.T) {
	var m map[string]clusterSetting
	data := "c:\n  reboot_completion_panic_threshold: 3h\n  reboot_completion_panic_action:\n    - mail:\n        - a@example.com\n        - b@example.com\n    - script1:\n        - touch /tmp/{:%fqdn%:}_panic\n    - script2:\n        - /bin/true\n"
	if err := yaml.Unmarshal([]byte(data), &m); err != nil {
		t.Fatal(err)
	}
	cs := m["c"]
	cs.mergePanicActionList()
	a := cs.RebootCompletionPanicActions
	if len(a.Mail) != 2 || a.Mail[0] != "a@example.com" || len(a.Scripts) != 2 || a.Scripts[0] != "touch /tmp/{:%fqdn%:}_panic" || a.Scripts[1] != "/bin/true" {
		t.Errorf("unexpected actions: %+v", a)
	}
}

func TestClusterDisabledWhenOffsetExceedsPanicThreshold(t *testing.T) {
	dir := t.TempDir()
	config = configSettings{LogBaseDir: dir + "/"}
	mainLogger = initLogger("goahead")
	clusterSettings = map[string]clusterSetting{}
	clusterLoggers = map[string]*logrus.Entry{}
	f := filepath.Join(dir, "c.yml")
	data := "bad:\n  enabled: true\n  reboot_completion_check_offset: 6h\n  reboot_completion_panic_threshold: 3h\nok:\n  enabled: true\n  reboot_completion_check_offset: 15m\n  reboot_completion_panic_threshold: 3h\n"
	if err := os.WriteFile(f, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	readClusterSetting(f)
	if clusterSettings["bad"].Enabled {
		t.Error("cluster with offset > threshold must be disabled")
	}
	if !clusterSettings["ok"].Enabled {
		t.Error("valid cluster must stay enabled")
	}
	log, _ := os.ReadFile(filepath.Join(dir, "bad.log"))
	if !strings.Contains(string(log), "level=error") || !strings.Contains(string(log), "automatically DISABLED") {
		t.Errorf("expected error log entry, got: %s", log)
	}
}

func TestPanicTimerSkipsDisabledCluster(t *testing.T) {
	dir := t.TempDir()
	mailOut := filepath.Join(dir, "mail.out")
	fakeSendmail := filepath.Join(dir, "sendmail")
	if err := os.WriteFile(fakeSendmail, []byte("#!/bin/sh\ncat > "+mailOut+"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	config = configSettings{SaveStateDir: dir + "/", SendmailPath: fakeSendmail, LogBaseDir: dir + "/"}
	clusterSettings = map[string]clusterSetting{"off": {
		Enabled:                        false,
		RebootCompletionPanicThreshold: time.Hour,
		RebootCompletionPanicActions:   rebootCompletionPanicActionsStruct{Mail: []string{"ops@example.com"}},
	}}
	clusterLoggers = map[string]*logrus.Entry{"off": initLogger("off")}
	state := clusterState{CurrentRestartingServers: map[string]struct{}{"h": {}}, LastRestartRequestTimestamp: time.Now().Add(-5 * time.Hour)}
	if err := writeStructJSONFile(filepath.Join(dir, "off.json"), state); err != nil {
		t.Fatal(err)
	}
	checkRebootPanicTimer("off")
	time.Sleep(300 * time.Millisecond)
	if fileExists(mailOut) {
		t.Error("disabled cluster must not send panic mail")
	}
	// same state on an enabled cluster fires
	c := clusterSettings["off"]
	c.Enabled = true
	clusterSettings["off"] = c
	checkRebootPanicTimer("off")
	time.Sleep(500 * time.Millisecond)
	if !fileExists(mailOut) {
		t.Error("enabled cluster should send panic mail")
	}
}
