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
	buildversion          string
	config                configSettings
	configFilePath        string
	clusterSettings       map[string]clusterSetting
	sleepingClusterChecks map[string]clusterCheck
	checkCluster          chan clusterCheck
	startCheckerChannel   chan request
	mutex                 sync.Mutex
	clusterLoggers        map[string]*logrus.Entry
	mainLogger            *logrus.Entry
	unknownLogger         *logrus.Entry
	checkerLogger         *logrus.Entry
)

func main() {

	var (
		configFileFlag = flag.String("config", "./config.yml", "which config file to use, defaults to ./config.yml")
		versionFlag    = flag.Bool("version", false, "show build time and version number")
	)
	forgetFlag := flag.String("forget", "", "remove this FQDN from the cluster state (and delete its ACK file), e.g. a deleted host that blocks its cluster, then exit")
	clusterFlag := flag.String("cluster", "", "with -forget: only forget the host in this cluster, defaults to all clusters")
	flag.BoolVar(&debug, "debug", false, "log debug output, defaults to false")
	flag.Parse()

	configFile := *configFileFlag
	version := *versionFlag

	if version {
		v := buildversion
		if v == "" {
			v = "dev" // plain go build without -ldflags -X main.buildversion=...
		}
		fmt.Println("goahead version "+v+" Build time:", buildtime, "UTC")
		os.Exit(0)
	}

	if *forgetFlag != "" {
		config = readConfigfile(configFile)
		if !forgetHost(*forgetFlag, *clusterFlag, logrus.NewEntry(logrus.StandardLogger())) {
			fmt.Println("Host " + *forgetFlag + " is not part of any restarting hosts in " + config.SaveStateDir)
			os.Exit(1)
		}
		os.Exit(0)
	}

	run(configFile)
}

// run reads the config file, loads the cluster settings and serves requests. It blocks forever and is separate from main() so the tests can start it with their own config.
func run(configFile string) {
	configFilePath = configFile
	config = readConfigfile(configFile)

	clusterLoggers = make(map[string]*logrus.Entry)
	clusterSettings = make(map[string]clusterSetting)
	checkCluster = make(chan clusterCheck)
	sleepingClusterChecks = make(map[string]clusterCheck)
	startCheckerChannel = make(chan request)

	mainLogger = initLogger("goahead")
	unknownLogger = initLogger("unknown")
	checkerLogger = initLogger("checker")

	if len(config.IncludeDir) > 0 {
		if isDir(config.IncludeDir) {
			mainLogger.Debug("Glob'ing with " + config.IncludeDir + "**/*.(yml|yaml)")
			files := []string{}
			err := filepath.Walk(config.IncludeDir, func(path string, f os.FileInfo, err error) error {
				mainLogger.Info("Checking for file extension on file: " + path)
				if filepath.Ext(path) == ".yml" || filepath.Ext(path) == ".yaml" {
					files = append(files, path)
				}
				return nil
			})

			if len(files) == 0 {
				mainLogger.Fatal("Could not find any cluster settings matching " + config.IncludeDir + "**/*.(yml|yaml)")
			}
			Debugf("found potential module versions:" + strings.Join(files, " "))
			if err != nil {
				mainLogger.Fatal("Failed to glob cluster settings include_dir with glob path " + config.IncludeDir + "**/*.(yml|yaml) Error: " + err.Error())
			}
			for _, f := range files {
				readClusterSetting(f)
			}
		}

	}

	mainLogger.Info("Found following cluster settings:")
	mainLogger.Infof("%+v\n", clusterSettings)

	// check for previously create cluster state files and check if I need to restart checker
	go checkCurrentClusterStates()

	// fire reboot completion panic actions even if no requests arrive anymore
	go startRebootPanicTimer()

	// release restarting hosts that were deleted (reboot_completion_release_removed_hosts)
	go startRemovedHostsTimer()

	go serve()

	select {}

}
