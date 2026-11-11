package main

import (
	"strconv"
	"strings"
	"time"
)

type clusterCheck struct {
	Csetting  clusterSetting
	Fqdn      string
	RequestID string
	Cluster   string
}

// scheduleCheckAfterOffset starts the reboot completion check after reboot_completion_check_offset, unless an inquire request of the host with a shorter uptime already started it (that removes the entry from sleepingClusterChecks). This way a host that never inquires again after its reboot does not block the cluster until the panic threshold.
func scheduleCheckAfterOffset(cc clusterCheck, req request) {
	time.AfterFunc(cc.Csetting.RebootCompletionCheckOffset, func() {
		mutex.Lock()
		current, ok := sleepingClusterChecks[cc.Fqdn]
		if ok && current.RequestID == cc.RequestID {
			delete(sleepingClusterChecks, cc.Fqdn)
		} else {
			ok = false
		}
		mutex.Unlock()
		if ok {
			checkerLogger.Info("reboot_completion_check_offset " + cc.Csetting.RebootCompletionCheckOffset.String() + " elapsed for FQDN: " + cc.Fqdn + " without a new inquire request, starting reboot completion check")
			startCheckForRebootedSystem(cc, req, cc.Csetting)
		}
	})
}

func startCheckForRebootedSystem(cc clusterCheck, req request, cs clusterSetting) {
	checkerLogger.Info("Starting check for rebooted system in cluster " + cc.Cluster + " with fqdn: " + cc.Fqdn)
	successfulChecks := 0
	for {
		if !hostStillRestarting(cc.Cluster, cc.Fqdn, checkerLogger) {
			checkerLogger.Info("fqdn: " + cc.Fqdn + " is not part of the restarting hosts of cluster " + cc.Cluster + " anymore (released, forgotten or removed), stopping its reboot completion check")
			return
		}
		command := strings.Replace(cc.Csetting.RebootCompletionCheck, "{:%fqdn%:}", cc.Fqdn, -1)
		command = strings.Replace(command, "{:%hostname%:}", cc.Fqdn, -1)
		command = strings.Replace(command, "{:%cluster%:}", cc.Fqdn, -1)
		er := executeCommand(command, 5, !cs.RaiseErrors, checkerLogger)
		checkerLogger.Info("Check result of "+command+" is ", er.returnCode)

		if er.returnCode == 0 {
			successfulChecks++
			checkerLogger.Info("Increasing successful check counter to " + strconv.Itoa(successfulChecks) + " of " + strconv.Itoa(cc.Csetting.RebootCompletionCheckConsecutiveSuccesses) + " for rebooted system in cluster " + cc.Cluster + " with fqdn: " + cc.Fqdn)
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
	clusterLogger := clusterLoggers[cc.Cluster]
	clusterLogger.Info("fqdn: " + cc.Fqdn + " seems to have successfully rebooted in cluster " + cc.Cluster)
	triggerRebootCompletionActions(cc.Fqdn, cc.Cluster, req.Uptime, clusterLogger)
	//deleteAckFile(cc.Fqdn, cc.Cluster)
	// decrement current restarts for cluster
	modifyClusterState(cc.Cluster, cc.Fqdn, "remove", clusterLogger)
	res := response{}
	res.Timestamp = time.Now()
	res.RequestingFqdn = cc.Fqdn
	res.ReportedUptime = req.Uptime
	res.FoundCluster = cc.Cluster
	res.Message = "fqdn: " + cc.Fqdn + " seems to have successfully rebooted in cluster " + cc.Cluster + " at " + res.Timestamp.String()
	saveAckFile(res, clusterLogger)
}
