package main

import (
	"io/ioutil"
	"time"

	yaml "gopkg.in/yaml.v2"
)

// ClusterSetting contains the key value pairs from the config file
type ClusterSetting struct {
	NamePattern                               string        `yaml:"name_pattern"`
	ClusterType                               string        `yaml:"cluster_type"`
	AllowedParallelRestarts                   int           `yaml:"allowed_parallel_restarts"`
	RebootCompletionCheck                     string        `yaml:"reboot_completion_check"`
	RebootCompletionCheckInterval             time.Duration `yaml:"reboot_completion_check_interval"`
	RebootCompletionCheckConsecutiveSuccesses string        `yaml:"reboot_completion_check_consecutive_successes"`
	RebootCompletionCheckOffset               time.Duration `yaml:"reboot_completion_check_offset"`
	MinimumUptime                             time.Duration `yaml:"minimum_uptime"`
	Enabled                                   bool          `yaml:"enabled"`
}

// readclusterSettingsFile creates the ConfigSettings struct from the config file
func readClusterSetting(clusterSettingsFile string) {
	Debugf("Trying to read cluster settings config file: " + clusterSettingsFile)
	data, err := ioutil.ReadFile(clusterSettingsFile)
	if err != nil {
		Fatalf("readclusterSettingsFile(): There was an error parsing the config file " + clusterSettingsFile + ": " + err.Error())
	}

	var cs map[string]ClusterSetting
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
