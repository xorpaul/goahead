package main

import (
	"io/ioutil"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
)

// clusterSetting contains the key value pairs from the config file
type clusterSetting struct {
	Enabled                                   bool          `yaml:"enabled"`
	NamePattern                               string        `yaml:"name_pattern"`
	BlacklistNamePattern                      []string      `yaml:"blacklist_name_pattern"`
	ClusterType                               string        `yaml:"cluster_type"`
	AllowedParallelRestarts                   int           `yaml:"allowed_parallel_restarts"`
	RebootCompletionCheck                     string        `yaml:"reboot_completion_check"`
	RebootCompletionCheckInterval             time.Duration `yaml:"reboot_completion_check_interval"`
	RebootCompletionCheckConsecutiveSuccesses int           `yaml:"reboot_completion_check_consecutive_successes"`
	RebootCompletionCheckOffset               time.Duration `yaml:"reboot_completion_check_offset"`
	RebootCompletionActions                   []string      `yaml:"reboot_completion_actions"`
	MinimumUptime                             time.Duration `yaml:"minimum_uptime"`
	RebootGoaheadActions                      []string      `yaml:"reboot_goahead_actions"`
	RebootGoaheadChecks                       []string      `yaml:"reboot_goahead_checks"`
	RebootGoaheadChecksExitCodeForReboot      int           `yaml:"reboot_goahead_checks_exit_code_for_reboot"`
}

// clusterState contains information over the cluster (how many nodes are currently restarting, when was the last cluster node restart, how many of the cluster nodes are up-to-date)
type clusterState struct {
	LastRestartRequestTimestamp    time.Time           `json:"last_restart_request_timestamp"`
	LastSuccessfulRestartTimestamp time.Time           `json:"last_successful_restart_timestamp"`
	CurrentOngoingRestarts         int                 `yaml:"current_ongoing_restarts"`
	CurrentRestartingServers       map[string]struct{} `yaml:"current_restarting_servers"`
}

// readclusterSettingsFile creates the ConfigSettings struct from the config file
func readClusterSetting(clusterSettingsFile string) {
	mainLogger.Debug("Trying to read cluster settings config file: " + clusterSettingsFile)
	data, err := ioutil.ReadFile(clusterSettingsFile)
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
		clusterSettings[clusterName] = clusterSetting
		clusterLogger := initLogger(clusterName)
		mutex.Lock()
		clusterLoggers[clusterName] = clusterLogger
		mutex.Unlock()
		clusterLogger.Infof("Using cluster settings: %+v", clusterSetting)
	}

	return
}

// triggerRebootGoaheadActions executes optional scripts that should run, when a host recieved the go_ahead to restart
func triggerRebootGoaheadActions(fqdn string, cluster string, uptime string, clusterLogger *logrus.Entry) {
	for _, action := range clusterSettings[cluster].RebootGoaheadActions {
		command := strings.Replace(action, "{:%fqdn%:}", fqdn, -1)
		command = strings.Replace(command, "{:%cluster%:}", cluster, -1)
		command = strings.Replace(command, "{:%uptime%:}", uptime, -1)
		command = strings.Replace(command, "{:%hostname%:}", strings.SplitN(fqdn, ".", 2)[0], -1)
		er := executeCommand(command, 5, true, clusterLogger)
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
		er := executeCommand(command, 5, true, clusterLogger)
		clusterLogger.Info("reboot completion action result of "+command+" is ", er.returnCode)
	}
}
