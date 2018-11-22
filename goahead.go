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
	debug                 bool
	verbose               bool
	info                  bool
	quiet                 bool
	buildtime             string
	config                configSettings
	clusterSettings       map[string]clusterSetting
	sleepingClusterChecks map[string]clusterCheck
	checkCluster          chan clusterCheck
	startCheckerChannel   chan string
	mutex                 sync.Mutex
	clusterLoggers        map[string]*logrus.Entry
	mainLogger            *logrus.Entry
	unknownLogger         *logrus.Entry
)

func main() {

	var (
		configFileFlag = flag.String("config", "./config.yml", "which config file to use")
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

	config = readConfigfile(configFile)

	clusterLoggers = make(map[string]*logrus.Entry)
	clusterSettings = make(map[string]clusterSetting)
	checkCluster = make(chan clusterCheck)
	sleepingClusterChecks = make(map[string]clusterCheck)
	startCheckerChannel = make(chan string)

	mainLogger = initLogger("goahead")
	unknownLogger = initLogger("unknown")

	if len(config.IncludeDir) > 0 {
		if isDir(config.IncludeDir) {
			globPath := filepath.Join(config.IncludeDir, "*.yml")
			mainLogger.Debug("Glob'ing with path " + globPath)
			matches, err := filepath.Glob(globPath)
			if len(matches) == 0 {
				mainLogger.Fatal("Could not find any cluster settings matching " + globPath)
			}
			Debugf("found potential module versions:" + strings.Join(matches, " "))
			if err != nil {
				mainLogger.Fatal("Failed to glob cluster settings include_dir with glob path " + globPath + " Error: " + err.Error())
			}
			for _, m := range matches {
				readClusterSetting(m)
			}
		}
	}

	mainLogger.Info("Found following cluster settings:")
	mainLogger.Infof("%+v\n", clusterSettings)

	// check for previously create cluster state files and check if I need to restart checker
	go checkCurrentClusterStates()

	go serve()
	go checkForRebootedSystems()

	select {}

}
