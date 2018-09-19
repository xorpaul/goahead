package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func SaveAckFile(res response) {
	folder := filepath.Join(config.SaveStateDir, res.FoundCluster)
	checkDirAndCreate(folder, "SaveAckFile")
	file := filepath.Join(folder, res.RequestingFqdn+"-"+res.RequestID+".json")
	Debugf("Trying to save ACK file " + file)

	f, err := os.Create(file)
	if err != nil {
		Fatalf("Could not write ACK file " + file + " " + err.Error())
	}
	defer f.Close()
	json, _ := json.Marshal(res)
	f.Write(json)
}

func LoadAckFile(req request) {
	// or use fqdn folder instead with the rids as files?
	//file := filepath.Join(config.SaveStateDir, req.Fqdn, req.RequestID)

}
