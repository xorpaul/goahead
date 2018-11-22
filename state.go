package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

type rebootCheckResult struct {
	FqdnGoAhead    bool
	ClusterGoAhead bool
	Reason         string
}

type inquireCheckResult struct {
	InquireToRestart bool
	Reason           string
}

func saveAckFile(res response, clusterLogger *logrus.Entry) {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	checkDirAndCreate(folder, "SaveAckFile cluster directory")
	file := filepath.Join(folder, res.RequestingFqdn+".json")
	clusterLogger.Debug("Trying to save fqdn ACK file " + file)
	writeStructJSONFile(file, res)
}

func checkAckFileInquire(req request, res response, clusterLogger *logrus.Entry) inquireCheckResult {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	file := filepath.Join(folder, req.Fqdn+".json")
	clusterLogger.Debug(res.RequestID + " Checking for ACK file " + file)
	if fileExists(file) {
		clusterLogger.Debug(res.RequestID + " Found ACK file " + file + " Trying to read it")
		var ackFile response
		ackFile = readAckFile(file, ackFile, res.FoundCluster, clusterLogger)
		if compareDurationString(req.Uptime, ackFile.ReportedUptime) == "shorter" {
			mutex.Lock()
			if _, ok := sleepingClusterChecks[res.RequestingFqdn]; ok {
				// Interrupt a reboot completion check if there is one still sleeping
				//clusterLogger.Info("Interrupting sleeping reboot completion check for " + request.Fqdn + " inside cluster " + res.FoundCluster)
				select {
				case startCheckerChannel <- req.Fqdn:
				default:
				}
			}
			mutex.Unlock()
		}
		saveAckFile(res, clusterLogger)
		if strings.HasPrefix(ackFile.Message, "YesInquireToRestart") {
			clusterLogger.Debug("YesInquireToRestart found for FQDN: " + req.Fqdn + " Reason: " + ackFile.Message)
			return inquireCheckResult{InquireToRestart: true, Reason: ackFile.Message}
		}
	} else {
		saveAckFile(res, clusterLogger)
	}
	return inquireCheckResult{InquireToRestart: false}
}

func checkChecksInquire(req request, res response, clusterLogger *logrus.Entry) inquireCheckResult {
	for _, check := range clusterSettings[res.FoundCluster].RebootGoaheadChecks {
		clusterLogger.Info("found goahead check:" + check)
		command := strings.Replace(check, "{:%fqdn%:}", req.Fqdn, -1)
		command = strings.Replace(command, "{:%cluster%:}", res.FoundCluster, -1)
		command = strings.Replace(command, "{:%hostname%:}", strings.SplitN(req.Fqdn, ".", 2)[0], -1)
		er := executeCommand(command, 5, true, clusterLogger)
		clusterLogger.Info("goahead check result of "+command+" is ", er.returnCode)
		if er.returnCode == clusterSettings[res.FoundCluster].RebootGoaheadChecksExitCodeForReboot {
			return inquireCheckResult{InquireToRestart: true, Reason: "YesInquireToRestart: goahead check result of " + command + " is " + strconv.Itoa(er.returnCode)}
		}
	}
	return inquireCheckResult{InquireToRestart: false}
}

func checkAckFile(req request, res response, clusterLogger *logrus.Entry) rebootCheckResult {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	file := filepath.Join(folder, req.Fqdn+".json")
	clusterLogger.Debug("Checking for ACK file " + file)
	if fileExists(file) && len(req.RequestID) > 1 {
		clusterLogger.Debug(res.RequestID + " Found ACK file " + file + " Trying to read it")
		var ackFile response
		ackFile = readAckFile(file, ackFile, res.FoundCluster, clusterLogger)
		if req.RequestID == ackFile.RequestID {
			clusterLogger.Debug(req.RequestID + " Found matching request_id in ACK file " + file + " and in request")
			return rebootCheckResult{FqdnGoAhead: true, ClusterGoAhead: false, Reason: ""}
		}
		return rebootCheckResult{FqdnGoAhead: false, ClusterGoAhead: false, Reason: "Found mismatching request_id in request: " + req.RequestID + " and found on middle-ware: " + ackFile.RequestID}
	}
	if len(req.RequestID) < 1 {
		res.Message = "Creating new request_id, because none was received"
		saveAckFile(res, clusterLogger)
	}
	return rebootCheckResult{FqdnGoAhead: false, ClusterGoAhead: false, Reason: "No previous request file found for fqdn: " + req.Fqdn}
}

func deleteAckFile(fqdn string, cluster string) {
	purgeDir(filepath.Join(config.SaveStateDir, cluster, fqdn+".json"), "successful reboot check for fqdn "+fqdn)
}

func checkClusterState(res response, result rebootCheckResult, clusterLogger *logrus.Entry) rebootCheckResult {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	checkDirAndCreate(folder, "SaveAckFile cluster directory")
	clusterFile := filepath.Join(config.SaveStateDir, res.FoundCluster+".json")
	mutex.Lock()
	defer mutex.Unlock()
	var cs clusterState
	if fileExists(clusterFile) {
		// read clusterFile and check if global clusterStates exists
		cs = readClusterStateFile(clusterFile, res.FoundCluster, clusterLogger)
		if _, ok := cs.CurrentRestartingServers[res.RequestingFqdn]; ok {
			result.Reason = "You should already be restarting!"
			result.ClusterGoAhead = false
			return result
		} else if cs.CurrentOngoingRestarts >= clusterSettings[res.FoundCluster].AllowedParallelRestarts {
			result.Reason = "Denied restart request as the current_ongoing_restarts of cluster " + res.FoundCluster + " is larger than the allowed_parallel_restarts: " + strconv.Itoa(cs.CurrentOngoingRestarts) + " >= " + strconv.Itoa(clusterSettings[res.FoundCluster].AllowedParallelRestarts) + " Currently restarting hosts: " + strings.Join(keysString(cs.CurrentRestartingServers), ",")
			result.ClusterGoAhead = false
			return result
		}
		cs.CurrentOngoingRestarts += 1
		cs.CurrentRestartingServers[res.RequestingFqdn] = struct{}{}
		cs.LastRestartRequestTimestamp = time.Now()
	} else {
		clusterLogger.Debug("Creating cluster state for cluster " + res.FoundCluster)
		crs := make(map[string]struct{})
		cs = clusterState{LastRestartRequestTimestamp: time.Now(), CurrentOngoingRestarts: 1, CurrentRestartingServers: crs}
		cs.CurrentRestartingServers[res.RequestingFqdn] = struct{}{}
	}
	clusterLogger.Debug("Trying to save cluster ACK file " + clusterFile)
	writeStructJSONFile(clusterFile, cs)
	result.ClusterGoAhead = true
	return result

}

func modifyClusterState(cluster string, fqdn string, operation string, clusterLogger *logrus.Entry) {
	clusterLogger.Info("Modifying cluster state for cluster: ", cluster, " for FQDN: ", fqdn, " with operation: ", operation)
	clusterFile := filepath.Join(config.SaveStateDir, cluster+".json")
	mutex.Lock()
	defer mutex.Unlock()
	var cs clusterState
	if fileExists(clusterFile) {
		cs = readClusterStateFile(clusterFile, cluster, clusterLogger)
		// server finished -> then --
		// server append -> then ++
		if operation == "remove" {
			cs.CurrentOngoingRestarts -= 1
			if cs.CurrentOngoingRestarts < 0 {
				cs.CurrentOngoingRestarts = 0
			}
			delete(cs.CurrentRestartingServers, fqdn)
			cs.LastSuccessfulRestartTimestamp = time.Now()
		} else if operation == "add" {
			cs.CurrentOngoingRestarts += 1
			cs.CurrentRestartingServers[fqdn] = struct{}{}
		} else {
			clusterLogger.Fatal("Invalid operation verb: " + operation + " for cluster state file: " + clusterFile)
		}

		clusterLogger.Debug("Trying to save cluster ACK file " + clusterFile)
		writeStructJSONFile(clusterFile, cs)
	} else {
		clusterLogger.Fatal("Could not find cluster state file to modify: " + clusterFile)
	}
}

// checkCurrentClusterStates checks the configured SaveStateDir for already existing cluster state files. Needed for a service restart to know the cluster state before the restart.
func checkCurrentClusterStates() {
	for cluster, csetting := range clusterSettings {
		mutex.Lock()
		clusterLogger := clusterLoggers[cluster]
		mutex.Unlock()
		go func(cluster string, csetting clusterSetting) {
			clusterFile := config.SaveStateDir + cluster + ".json"
			if fileExists(clusterFile) {
				clusterLogger.Info("Found previously existing cluster state file: " + clusterFile)
				var cs clusterState
				cs = readClusterStateFile(clusterFile, cluster, clusterLogger)
				if cs.CurrentOngoingRestarts > 0 || len(cs.CurrentRestartingServers) > 0 {
					clusterLogger.Info("Trying to restart cluster node checks for clusterFile: " + clusterFile)
					// restart the successfull reboot checker, otherwise it would block _all_ later restart requests
					for restartingClusterNode := range cs.CurrentRestartingServers {
						clusterLogger.Info("Restarting cluster checker for " + restartingClusterNode + " inside cluster " + cluster + " check command: " + csetting.RebootCompletionCheck)
						select {
						case checkCluster <- clusterCheck{csetting, restartingClusterNode, "restartingClusterCheckAtStartup fqdn: " + restartingClusterNode + " cluster: " + cluster, cluster}:
						default:
						}
					}

				}
			}
		}(cluster, csetting)
	}
}
