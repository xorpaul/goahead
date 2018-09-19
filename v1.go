package main

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

type request struct {
	Fqdn      string `json:"fqdn"`
	Uptime    string `json:"uptime"`
	RequestID string `json:"request_id"`
}

type response struct {
	Timestamp      time.Time `json:"timestamp"`
	Goahead        bool      `json:"go_ahead"`
	UnknownHost    bool      `json:"unknown_host"`
	AskagainIn     string    `json:"ask_again_in"`
	RequestID      string    `json:"request_id"`
	FoundCluster   string    `json:"found_cluster"`
	RequestingFqdn string    `json:"requesting_fqdn"`
}

type clusterCheck struct {
	ClusterSetting ClusterSetting
	Response       response
}

func respondWithJSON(w http.ResponseWriter, code int, rid string, payload interface{}) {
	response, _ := json.Marshal(payload)

	Debugf(rid + " Sending response " + string(response))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func respondWithError(w http.ResponseWriter, code int, rid string, message string) {
	respondWithJSON(w, code, rid, map[string]string{"error": message})
}

// RequestHandlerV1 is a v1-compatible version of ExampleHandler
func RestartHandlerV1(w http.ResponseWriter, r *http.Request) {
	timestamp := time.Now()
	ip := strings.Split(r.RemoteAddr, ":")[0]
	method := r.Method
	rid := randSeq()
	//bodyBytes, err := ioutil.ReadAll(r.Body)
	//if err != nil {
	//	Warnf(rid + " Could not read HTTP body!")
	//	respondWithError(w, http.StatusBadRequest, rid, "Invalid request payload")
	//}

	Debugf(rid + " Incoming " + method + " request from IP: " + ip)
	var request request
	var res response
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		//Warnf(rid + " Could not parse JSON request: " + string(bodyBytes))
		respondWithError(w, http.StatusBadRequest, rid, "Invalid request payload")
		return
	}
	defer r.Body.Close()

	if len(request.Fqdn) < 1 || len(request.Uptime) < 1 {
		respondWithError(w, http.StatusBadRequest, rid, "Invalid request payload. Need at least fqdn and uptime fields!")
		return
	}
	Debugf(rid + " Received request with fqdn: " + request.Fqdn + " and uptime: " + string(request.Uptime))

	uptime, err := time.ParseDuration(request.Uptime)
	if err != nil {
		uptime_error := "Can not convert value " + request.Uptime + " of your uptime to a golang Duration. Valid time units are 300ms, 1.5h or 2h45m."
		Warnf(rid + " " + uptime_error)
		respondWithError(w, http.StatusBadRequest, rid, uptime_error)
		return
	}

	// Default response fields
	res.Timestamp = timestamp
	res.RequestID = rid
	res.RequestingFqdn = request.Fqdn
	// Default response if cluster/fqdn is unknown
	res.AskagainIn = strconv.Itoa(rand.Intn(500)) + "s"
	res.Goahead = false
	res.UnknownHost = true
	for c := range clusterSettings {
		Debugf(rid + " Checking against Cluster setting " + c)
		if m := regexp.MustCompile(clusterSettings[c].NamePattern).FindStringSubmatch(request.Fqdn); len(m) > 1 {
			Debugf(rid + " Found matching name pattern " + clusterSettings[c].NamePattern + " with fqdn from request " + request.Fqdn)
			// TODO somehow check if this request was here previously (database, file?)
			// add some dir to config and create fqdn + rid file to check this
			// delete afterwards and set goahead to true
			// then create a clustername file to lock this cluster if >= max parallel restarts
			res.UnknownHost = false
			res.FoundCluster = c
			res.Goahead = false

			if uptime.Seconds() < clusterSettings[c].MinimumUptime.Seconds() {
				res.AskagainIn = time.Duration.String(clusterSettings[c].MinimumUptime)
				Debugf(rid + " Configured minimum uptime for cluster: " + time.Duration.String(clusterSettings[c].MinimumUptime) + " was not reached by client's uptime: " + request.Uptime)
				respondWithJSON(w, http.StatusOK, rid, res)
				return
			}

			if len(request.RequestID) < 1 {
				res.AskagainIn = strconv.Itoa(rand.Intn(20)) + "s"
				SaveAckFile(res)
			} else {
				file := filepath.Join(config.SaveStateDir, res.FoundCluster, request.Fqdn+"-"+request.RequestID+".json")
				Debugf(rid + " Checking for ACK file " + file)
				if fileExists(file) {
					res.Goahead = true
					res.AskagainIn = "0s"
					res.RequestID = request.RequestID
					SaveAckFile(res)
					select {
					case checkCluster <- clusterCheck{clusterSettings[c], res}:
						Debugf(rid + " Activating cluster checker for " + request.Fqdn + " inside cluster " + res.FoundCluster)
					default:
					}
				}
			}

			respondWithJSON(w, http.StatusOK, rid, res)
			return
		} else {
			Debugf(rid + " Name pattern " + clusterSettings[c].NamePattern + " does not match with fqdn from request " + request.Fqdn)
		}
	}
	respondWithJSON(w, http.StatusOK, rid, res)

}

// AddV1Routes takes a router or subrouter and adds all the v1 routes to it
func AddV1Routes(r *mux.Router) {
	r.HandleFunc("/request/restart/os", RestartHandlerV1)
	AddRoutes(r)
}

// AddRoutes takes a router or subrouter and adds all the latest routes to it
func AddRoutes(r *mux.Router) {
	r.HandleFunc("/request/restart/", RestartHandlerV1)
}
