package main

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
)

type rebootCompletionPanicActionsStruct struct {
	Mail    []string `yaml:"mail"`
	Scripts []string `yaml:"scripts"`
}

// clusterSetting contains the key value pairs from the config file
type clusterSetting struct {
	Enabled                                   bool                               `yaml:"enabled"`
	NamePattern                               string                             `yaml:"name_pattern"`
	BlacklistNamePattern                      []string                           `yaml:"blacklist_name_pattern"`
	ClusterType                               string                             `yaml:"cluster_type"`
	AllowedParallelRestarts                   int                                `yaml:"allowed_parallel_restarts"`
	RebootCompletionCheck                     string                             `yaml:"reboot_completion_check"`
	RebootCompletionCheckInterval             time.Duration                      `yaml:"reboot_completion_check_interval"`
	RebootCompletionCheckConsecutiveSuccesses int                                `yaml:"reboot_completion_check_consecutive_successes"`
	RebootCompletionCheckOffset               time.Duration                      `yaml:"reboot_completion_check_offset"`
	RebootCompletionActions                   []string                           `yaml:"reboot_completion_actions"`
	RebootCompletionPanicThreshold            time.Duration                      `yaml:"reboot_completion_panic_threshold"`
	ReleaseRemovedHosts                       bool                               `yaml:"reboot_completion_release_removed_hosts"`
	RemovedHostCheck                          string                             `yaml:"reboot_completion_removed_host_check"`
	RemovedHostGrace                          time.Duration                      `yaml:"reboot_completion_removed_host_grace"`
	RebootCompletionPanicActions              rebootCompletionPanicActionsStruct `yaml:"reboot_completion_panic_actions"`
	// RebootCompletionPanicActionList is the list format used by the cluster configs of other tools:
	// a list of single key maps, where the key "mail" holds mail addresses and every other key (script1, script2, ...) holds commands
	RebootCompletionPanicActionList      []map[string][]string `yaml:"reboot_completion_panic_action"`
	MinimumUptime                        time.Duration         `yaml:"minimum_uptime"`
	RebootGoaheadActions                 []string              `yaml:"reboot_goahead_actions"`
	RebootGoaheadChecks                  []string              `yaml:"reboot_goahead_checks"`
	RebootGoaheadChecksExitCodeForReboot int                   `yaml:"reboot_goahead_checks_exit_code_for_reboot"`
	RaiseErrors                          bool                  `yaml:"raise_errors"`
	ConfigFile                           string                `yaml:"-"`
}

// clusterState contains information over the cluster (how many nodes are currently restarting, when was the last cluster node restart, how many of the cluster nodes are up-to-date)
type clusterState struct {
	LastRestartPanicTimestamp      time.Time           `json:"last_restart_panic_timestamp"`
	LastRestartRequestTimestamp    time.Time           `json:"last_restart_request_timestamp"`
	LastSuccessfulRestartTimestamp time.Time           `json:"last_successful_restart_timestamp"`
	CurrentOngoingRestarts         int                 `yaml:"current_ongoing_restarts"`
	CurrentRestartingServers       map[string]struct{} `yaml:"current_restarting_servers"`
}

// readclusterSettingsFile creates the ConfigSettings struct from the config file
func readClusterSetting(clusterSettingsFile string) {
	mainLogger.Debug("Trying to read cluster settings config file: " + clusterSettingsFile)
	data, err := os.ReadFile(clusterSettingsFile)
	if err != nil {
		mainLogger.Fatal("readclusterSettingsFile(): There was an error parsing the config file " + clusterSettingsFile + ": " + err.Error())
	}

	var cs map[string]clusterSetting
	err = yaml.Unmarshal([]byte(data), &cs)
	if err != nil {
		mainLogger.Fatal("In file " + clusterSettingsFile + ": YAML unmarshal error: " + err.Error())
	}

	for clusterName, clusterSetting := range cs {
		mainLogger.Debug("Adding cluster settings " + clusterName)
		clusterSetting.ConfigFile = clusterSettingsFile
		clusterSetting.mergePanicActionList()
		clusterLogger := initLogger(clusterName)
		clusterLoggers[clusterName] = clusterLogger
		if clusterSetting.Enabled && clusterSetting.RebootCompletionPanicThreshold > 0 && clusterSetting.RebootCompletionCheckOffset > clusterSetting.RebootCompletionPanicThreshold {
			msg := "Cluster " + clusterName + " is automatically DISABLED: reboot_completion_check_offset (" + clusterSetting.RebootCompletionCheckOffset.String() +
				") is larger than reboot_completion_panic_threshold (" + clusterSetting.RebootCompletionPanicThreshold.String() + ") in " + clusterSettingsFile +
				". The reboot completion check would not even start polling before the panic actions fire for a host that is still rebooting. " +
				"Lower reboot_completion_check_offset or raise reboot_completion_panic_threshold to enable the cluster."
			clusterLogger.Error(msg)
			mainLogger.Error(msg)
			clusterSetting.Enabled = false
		}
		clusterSettings[clusterName] = clusterSetting
		clusterLogger.Infof("Using cluster settings: %+v", clusterSetting)
	}
}

// triggerRebootGoaheadActions executes optional scripts that should run, when a host recieved the go_ahead to restart
func triggerRebootGoaheadActions(fqdn string, cluster string, uptime string, clusterLogger *logrus.Entry) {
	for _, action := range clusterSettings[cluster].RebootGoaheadActions {
		command := strings.Replace(action, "{:%fqdn%:}", fqdn, -1)
		command = strings.Replace(command, "{:%cluster%:}", cluster, -1)
		command = strings.Replace(command, "{:%uptime%:}", uptime, -1)
		command = strings.Replace(command, "{:%hostname%:}", strings.SplitN(fqdn, ".", 2)[0], -1)
		er := executeCommand(command, 5, !clusterSettings[cluster].RaiseErrors, clusterLogger)
		clusterLogger.Info("goahead action result of "+command+" is ", er.returnCode)
	}
}

// triggerRebootCompletionActions executes optional scripts that should run, when a host is flagged as sucsessfully rebooted
func triggerRebootCompletionActions(fqdn string, cluster string, uptime string, clusterLogger *logrus.Entry) {
	for _, action := range clusterSettings[cluster].RebootCompletionActions {
		clusterLogger.Info("found reboot completion action:" + action)
		command := strings.Replace(action, "{:%fqdn%:}", fqdn, -1)
		command = strings.Replace(command, "{:%cluster%:}", cluster, -1)
		command = strings.Replace(command, "{:%uptime%:}", uptime, -1)
		command = strings.Replace(command, "{:%hostname%:}", strings.SplitN(fqdn, ".", 2)[0], -1)
		er := executeCommand(command, 5, !clusterSettings[cluster].RaiseErrors, clusterLogger)
		clusterLogger.Info("reboot completion action result of "+command+" is ", er.returnCode)
	}
}

// mergePanicActionList adds the entries of the list format reboot_completion_panic_action to RebootCompletionPanicActions
func (cs *clusterSetting) mergePanicActionList() {
	for _, entry := range cs.RebootCompletionPanicActionList {
		keys := make([]string, 0, len(entry))
		for key := range entry {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if key == "mail" {
				cs.RebootCompletionPanicActions.Mail = append(cs.RebootCompletionPanicActions.Mail, entry[key]...)
			} else {
				cs.RebootCompletionPanicActions.Scripts = append(cs.RebootCompletionPanicActions.Scripts, entry[key]...)
			}
		}
	}
}
