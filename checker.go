package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type clusterCheck struct {
	Csetting  clusterSetting
	Fqdn      string
	RequestID string
	Cluster   string
}

func checkForRebootedSystems() {
	var checkerLog = logrus.New()
	file, err := os.OpenFile(filepath.Join(config.LogBaseDir, "checker.log"), os.O_CREATE|os.O_WRONLY, 0666)
	if err == nil {
		checkerLog.Out = file
	} else {
		checkerLog.Fatal("Failed to log to file " + filepath.Join(config.LogBaseDir, "checker.log") + " Error: " + err.Error())
	}
	if debug {
		checkerLog.SetLevel(logrus.DebugLevel)
	}

	checkerLog.Debug("Starting checker")

	for cc := range checkCluster {
		go func(cc clusterCheck, startCheckerChannel chan string) {
			checkerLogger := checkerLog.WithFields(logrus.Fields{"request_id": cc.RequestID, "cluster": cc.Cluster})

			checkerLogger.Info("Sleeping for reboot_completion_check_offset: " + cc.Csetting.RebootCompletionCheckOffset.String())
			select {
			case fqdn := <-startCheckerChannel:
				if fqdn == cc.Fqdn {
					checkerLogger.Info("Received inquire request from FQDN " + cc.Fqdn + " Interrupting reboot_completion_check_offset sleep!")
					break
				}
			case <-time.After(cc.Csetting.RebootCompletionCheckOffset):
				checkerLogger.Info("Waking up from time.After sleep!")
				break
			}

			checkerLogger.Debug("Starting check for rebooted system in cluster " + cc.Cluster + " with fqdn: " + cc.Fqdn)
			successfulChecks := 0
			for {
				command := strings.Replace(cc.Csetting.RebootCompletionCheck, "{:%fqdn%:}", cc.Fqdn, -1)
				checkerLogger.Info("Executing check: " + command)
				er := executeCommand(command, 5, true)
				checkerLogger.Info("check result of "+command+" is", er.returnCode)

				if er.returnCode == 0 {
					successfulChecks += 1
					checkerLogger.Debug("Increasing successful check counter to " + strconv.Itoa(successfulChecks) + " for rebooted system in cluster " + cc.Cluster + " with fqdn: " + cc.Fqdn)
					if successfulChecks >= cc.Csetting.RebootCompletionCheckConsecutiveSuccesses {
						break
					}
				} else {
					successfulChecks = 0
				}
				checkerLogger.Debug("Sleeping for reboot_completion_check_interval: " + cc.Csetting.RebootCompletionCheckInterval.String())
				time.Sleep(cc.Csetting.RebootCompletionCheckInterval)
			}
			checkerLogger.Debug("fqdn: " + cc.Fqdn + " seems to have successfully rebooted in cluster " + cc.Cluster)
			deleteAckFile(cc.Fqdn, cc.Cluster)
			// decrement current restarts for cluster
			modifyClusterState(cc.Cluster, cc.Fqdn, "remove")
		}(cc, startCheckerChannel)

	}
}
