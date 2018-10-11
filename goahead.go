package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	debug               bool
	verbose             bool
	info                bool
	quiet               bool
	buildtime           string
	config              configSettings
	clusterSettings     map[string]clusterSetting
	checkCluster        chan clusterCheck
	startCheckerChannel chan string
	mutex               sync.Mutex
	mainLog             *logrus.Logger
)

func main() {

	var (
		configFileFlag = flag.String("config", "/etc/goahead/config.yml", "which config file to use")
		versionFlag    = flag.Bool("version", false, "show build time and version number")
	)
	flag.BoolVar(&debug, "debug", false, "log debug output, defaults to false")
	flag.Parse()

	configFile := *configFileFlag
	version := *versionFlag

	if version {
		fmt.Println("goahead version 0.0.1 Build time:", buildtime, "UTC")
		os.Exit(0)
	}

	var mainLog = logrus.New()
	if debug {
		mainLog.SetLevel(logrus.DebugLevel)
	}
	// until the config was parsed to get the logfile destination
	mainLog.Out = os.Stdout

	Debugf("Using as config file: " + configFile)
	config = readConfigfile(configFile)
	file, err := os.OpenFile(filepath.Join(config.LogBaseDir, "goahead.log"), os.O_CREATE|os.O_WRONLY, 0666)
	if err == nil {
		mainLog.Out = file
	} else {
		mainLog.Fatal("Failed to log to file " + filepath.Join(config.LogBaseDir, "goahead.log") + " Error: " + err.Error())
	}

	clusterSettings = make(map[string]clusterSetting)
	checkCluster = make(chan clusterCheck)
	startCheckerChannel = make(chan string)
	if len(config.IncludeDir) > 0 {
		if isDir(config.IncludeDir) {
			globPath := filepath.Join(config.IncludeDir, "*.yml")
			mainLog.Debug("Glob'ing with path " + globPath)
			matches, err := filepath.Glob(globPath)
			if len(matches) == 0 {
				mainLog.Fatal("Could not find any cluster settings matching " + globPath)
			}
			Debugf("found potential module versions:" + strings.Join(matches, " "))
			if err != nil {
				mainLog.Fatal("Failed to glob cluster settings include_dir with glob path " + globPath + " Error: " + err.Error())
			}
			for _, m := range matches {
				readClusterSetting(m)
			}
		}
	}

	mainLog.Info("Found following cluster settings:")
	mainLog.Info("%+v\n", clusterSettings)

	// check for previously create cluster state files and check if I need to restart checker
	go checkCurrentClusterStates()

	go serve()
	go checkForRebootedSystems()

	select {}

}
