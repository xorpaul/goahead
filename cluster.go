package main

import (
	"io/ioutil"
	"time"

	yaml "gopkg.in/yaml.v2"
)

// clusterSetting contains the key value pairs from the config file
type clusterSetting struct {
	NamePattern                               string        `yaml:"name_pattern"`
	BlacklistNamePattern                      []string      `yaml:"blacklist_name_pattern"`
	ClusterType                               string        `yaml:"cluster_type"`
	AllowedParallelRestarts                   int           `yaml:"allowed_parallel_restarts"`
	RebootCompletionCheck                     string        `yaml:"reboot_completion_check"`
	RebootCompletionCheckInterval             time.Duration `yaml:"reboot_completion_check_interval"`
	RebootCompletionCheckConsecutiveSuccesses int           `yaml:"reboot_completion_check_consecutive_successes"`
	RebootCompletionCheckOffset               time.Duration `yaml:"reboot_completion_check_offset"`
	MinimumUptime                             time.Duration `yaml:"minimum_uptime"`
	Enabled                                   bool          `yaml:"enabled"`
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
	Debugf("Trying to read cluster settings config file: " + clusterSettingsFile)
	data, err := ioutil.ReadFile(clusterSettingsFile)
	if err != nil {
		Fatalf("readclusterSettingsFile(): There was an error parsing the config file " + clusterSettingsFile + ": " + err.Error())
	}

	var cs map[string]clusterSetting
	err = yaml.Unmarshal([]byte(data), &cs)
	if err != nil {
		Fatalf("In file " + clusterSettingsFile + ": YAML unmarshal error: " + err.Error())
	}

	//fmt.Print("config: ")
	//fmt.Printf("%+v\n", cs)
	for clusterName, clusterSetting := range cs {
		Debugf("Adding cluster settings " + clusterName)
		//fmt.Printf("%+v\n", clusterName)
		//fmt.Printf("%+v\n", clusterSetting)
		clusterSettings[clusterName] = clusterSetting
	}

	return
}
