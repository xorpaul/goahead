package main

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

type clusterCheck struct {
	Csetting  clusterSetting
	Fqdn      string
	RequestID string
	Cluster   string
}

func checkForRebootedSystems() {
	checkerLogger := initLogger("checker")
	var wgChecker sync.WaitGroup

	for cc := range checkCluster {
		wgChecker.Add(1)
		go func(cc clusterCheck) {
			defer wgChecker.Done()
			//checkerLogger.Warn("Recieved: ", cc)
			checkerLogger.Info("Sleeping for reboot_completion_check_offset: " + cc.Csetting.RebootCompletionCheckOffset.String() + " for FQDN: " + cc.Fqdn)
			mutex.Lock()
			sleepingClusterChecks[cc.Fqdn] = cc
			mutex.Unlock()

			req := request{}
			select {
			case <-time.After(cc.Csetting.RebootCompletionCheckOffset):
				checkerLogger.Info("Waking up check from time.After sleep for " + cc.Csetting.RebootCompletionCheckOffset.String())
				// TODO do panic stuff
				break
			case req = <-startCheckerChannel:
				checkerLogger.Info("Received inquire request from FQDN " + req.Fqdn + " Interrupting reboot_completion_check_offset sleep!")
				mutex.Lock()
				delete(sleepingClusterChecks, cc.Fqdn)
				mutex.Unlock()
				break
			}

			checkerLogger.Debug("Starting check for rebooted system in cluster " + cc.Cluster + " with fqdn: " + cc.Fqdn)
			successfulChecks := 0
			for {
				command := strings.Replace(cc.Csetting.RebootCompletionCheck, "{:%fqdn%:}", cc.Fqdn, -1)
				command = strings.Replace(command, "{:%hostname%:}", cc.Fqdn, -1)
				command = strings.Replace(command, "{:%cluster%:}", cc.Fqdn, -1)
				er := executeCommand(command, 5, true, checkerLogger)
				checkerLogger.Info("Check result of "+command+" is ", er.returnCode)

				if er.returnCode == 0 {
					successfulChecks++
					checkerLogger.Info("Increasing successful check counter to " + strconv.Itoa(successfulChecks) + " for rebooted system in cluster " + cc.Cluster + " with fqdn: " + cc.Fqdn)
					if successfulChecks >= cc.Csetting.RebootCompletionCheckConsecutiveSuccesses {
						break
					}
				} else {
					successfulChecks = 0
				}
				//checkerLogger.Info("Sleeping for reboot_completion_check_interval: " + cc.Csetting.RebootCompletionCheckInterval.String())
				time.Sleep(cc.Csetting.RebootCompletionCheckInterval)
			}
			checkerLogger.Info("fqdn: " + cc.Fqdn + " seems to have successfully rebooted in cluster " + cc.Cluster)
			mutex.Lock()
			clusterLogger := clusterLoggers[cc.Cluster]
			mutex.Unlock()
			clusterLogger.Info("fqdn: " + cc.Fqdn + " seems to have successfully rebooted in cluster " + cc.Cluster)
			triggerRebootCompletionActions(cc.Fqdn, cc.Cluster, req.Uptime, clusterLogger)
			//deleteAckFile(cc.Fqdn, cc.Cluster)
			// decrement current restarts for cluster
			modifyClusterState(cc.Cluster, cc.Fqdn, "remove", clusterLogger)
		}(cc)
	}
	wgChecker.Wait()
}
