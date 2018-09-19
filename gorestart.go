package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	debug           bool
	verbose         bool
	info            bool
	quiet           bool
	buildtime       string
	config          ConfigSettings
	clusterSettings map[string]ClusterSetting
	checkCluster    chan clusterCheck
)

func main() {

	var (
		configFileFlag = flag.String("config", "/etc/gorestart/config.yml", "which config file to use")
		versionFlag    = flag.Bool("version", false, "show build time and version number")
	)
	flag.BoolVar(&debug, "debug", false, "log debug output, defaults to false")
	flag.Parse()

	configFile := *configFileFlag
	version := *versionFlag

	if version {
		fmt.Println("gorestart version 0.0.1 Build time:", buildtime, "UTC")
		os.Exit(0)
	}

	Debugf("Using as config file: " + configFile)
	config = readConfigfile(configFile)

	clusterSettings = make(map[string]ClusterSetting)
	checkCluster = make(chan clusterCheck)
	if len(config.IncludeDir) > 0 {
		if isDir(config.IncludeDir) {
			globPath := filepath.Join(config.IncludeDir, "/*.yml")
			Debugf("Glob'ing with path " + globPath)
			matches, err := filepath.Glob(globPath)
			if len(matches) == 0 {
				Fatalf("Could not find any cluster settings matching " + globPath)
			}
			Debugf("found potential module versions:" + strings.Join(matches, " "))
			if err != nil {
				Fatalf("Failed to glob cluster settings include_dir with glob path " + globPath + " Error: " + err.Error())
			}
			for _, m := range matches {
				readClusterSetting(m)
			}
		}
	}

	fmt.Printf("%+v\n", clusterSettings)

	go serve()
	go checkForRebootedSystems()

	select {}

}
