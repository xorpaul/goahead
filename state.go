package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

func saveAckFile(res response) {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	checkDirAndCreate(folder, "SaveAckFile cluster directory")
	file := filepath.Join(folder, res.RequestingFqdn+".json")
	mainLogger.Debug("Trying to save fqdn ACK file " + file)
	writeStructJSONFile(file, res)
}

func checkAckFileInquire(req request, res response) inquireCheckResult {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	file := filepath.Join(folder, req.Fqdn+".json")
	mainLogger.Debug(res.RequestID + " Checking for ACK file " + file)
	fmt.Println(req)
	if fileExists(file) {
		mainLogger.Debug(res.RequestID + " Found ACK file " + file + " Trying to read it")
		var ackFile response
		ackFile = readAckFile(file, ackFile)
		if strings.HasPrefix(ackFile.Message, "YesInquireToRestart") {
			mainLogger.Debug("YesInquireToRestart found for FQDN: " + req.Fqdn + " Reason: " + ackFile.Message)
			return inquireCheckResult{InquireToRestart: true, Reason: ackFile.Message}
		}
		// Interrupt a reboot completion check if there is one still sleeping
		startCheckerChannel <- req.Fqdn
	}
	return inquireCheckResult{InquireToRestart: false}
}

func checkAckFile(req request, res response) rebootCheckResult {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	file := filepath.Join(folder, req.Fqdn+".json")
	mainLogger.Debug(res.RequestID + " Checking for ACK file " + file)
	if fileExists(file) && len(req.RequestID) > 1 {
		mainLogger.Debug(res.RequestID + " Found ACK file " + file + " Trying to read it")
		var ackFile response
		ackFile = readAckFile(file, ackFile)
		//fmt.Println(ackFile)
		if req.RequestID == ackFile.RequestID {
			mainLogger.Debug(req.RequestID + " Found matching request_id in ACK file " + file + " and in request")
			return rebootCheckResult{FqdnGoAhead: true, ClusterGoAhead: false, Reason: ""}
		}
		return rebootCheckResult{FqdnGoAhead: false, ClusterGoAhead: false, Reason: "Found mismatching request_id in request: " + req.RequestID + " and found on middle-ware: " + ackFile.RequestID}
	}
	if len(req.RequestID) < 1 {
		saveAckFile(res)
	}
	return rebootCheckResult{FqdnGoAhead: false, ClusterGoAhead: false, Reason: "No previous request file found for fqdn: " + req.Fqdn}
}

func deleteAckFile(fqdn string, cluster string) {
	purgeDir(filepath.Join(config.SaveStateDir, cluster, fqdn+".json"), "successful reboot check for fqdn "+fqdn)
}

func checkClusterState(res response, result rebootCheckResult) rebootCheckResult {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	checkDirAndCreate(folder, "SaveAckFile cluster directory")
	clusterFile := filepath.Join(config.SaveStateDir, res.FoundCluster+".json")
	mutex.Lock()
	defer mutex.Unlock()
	var cs clusterState
	if fileExists(clusterFile) {
		// read clusterFile and check if global clusterStates exists
		cs = readClusterStateFile(clusterFile)
		fmt.Println(cs)
		if cs.CurrentOngoingRestarts >= clusterSettings[res.FoundCluster].AllowedParallelRestarts {
			reason := "Denied restart request as the current_ongoing_restarts of cluster " + res.FoundCluster + " is >= the allowed_parallel_restarts: " + strconv.Itoa(cs.CurrentOngoingRestarts) + " >= " + strconv.Itoa(clusterSettings[res.FoundCluster].AllowedParallelRestarts) + " Currently restarting hosts: " + strings.Join(keysString(cs.CurrentRestartingServers), ",")
			result.ClusterGoAhead = false
			result.Reason = reason
			return result
		} else {
			cs.CurrentOngoingRestarts += 1
			cs.CurrentRestartingServers[res.RequestingFqdn] = struct{}{}
			cs.LastRestartRequestTimestamp = time.Now()
		}
	} else {
		mainLogger.Debug("Creating cluster state for cluster " + res.FoundCluster)
		crs := make(map[string]struct{})
		cs = clusterState{LastRestartRequestTimestamp: time.Now(), CurrentOngoingRestarts: 1, CurrentRestartingServers: crs}
		cs.CurrentRestartingServers[res.RequestingFqdn] = struct{}{}
	}
	mainLogger.Debug("Trying to save cluster ACK file " + clusterFile)
	writeStructJSONFile(clusterFile, cs)
	result.ClusterGoAhead = true
	return result

}

func modifyClusterState(cluster string, fqdn string, operation string) {
	clusterFile := filepath.Join(config.SaveStateDir, cluster+".json")
	mutex.Lock()
	defer mutex.Unlock()
	var cs clusterState
	if fileExists(clusterFile) {
		cs = readClusterStateFile(clusterFile)
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
			mainLogger.Fatal("Invalid operation verb: " + operation + " for cluster state file: " + clusterFile)
		}

		mainLogger.Debug("Trying to save cluster ACK file " + clusterFile)
		writeStructJSONFile(clusterFile, cs)
	} else {
		mainLogger.Fatal("Could not find cluster state file to modify: " + clusterFile)
	}
}

// checkCurrentClusterStates checks the configured SaveStateDir for already existing cluster state files. Needed for a service restart to know the cluster state before the restart.
func checkCurrentClusterStates() {
	for cluster, csetting := range clusterSettings {
		clusterFile := config.SaveStateDir + cluster + ".json"
		if fileExists(clusterFile) {
			mainLogger.Debug("Found previously existing cluster state file: " + clusterFile)
			var cs clusterState
			cs = readClusterStateFile(clusterFile)
			if cs.CurrentOngoingRestarts > 0 || len(cs.CurrentRestartingServers) > 0 {
				mainLogger.Debug("Trying to restart cluster node checks for clusterFile: " + clusterFile)
				// restart the successfull reboot checker, otherwise it would block _all_ later restart requests
				for restartingClusterNode := range cs.CurrentRestartingServers {
					mainLogger.Debug("Restarting cluster checker for " + restartingClusterNode + " inside cluster " + cluster + " check command: " + csetting.RebootCompletionCheck)
					fmt.Println("checkCurrentClusterStates()", csetting)
					checkCluster <- clusterCheck{csetting, restartingClusterNode, "restartingClusterCheckAtStartup fqdn: " + restartingClusterNode + " cluster: " + cluster, cluster}
				}

			}
		}
	}
}
