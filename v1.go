package main

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
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
	Message        string    `json:"message"`
}

func respondWithJSON(w http.ResponseWriter, code int, rid string, payload interface{}) {
	response, _ := json.Marshal(payload)

	mainLogger.Info("Sending response " + string(response))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

func respondWithError(w http.ResponseWriter, code int, rid string, message string) {
	respondWithJSON(w, code, rid, map[string]string{"error": message})
}

// healthHandler is a simple service health handler
func healthHandler(w http.ResponseWriter, r *http.Request) {

	var res response
	res.Timestamp = time.Now()
	res.RequestID = randSeq()
	res.Message = "HealthHandler!"
	res.AskagainIn = strconv.Itoa(rand.Intn(500)) + "s"
	res.UnknownHost = true

	respondWithJSON(w, http.StatusOK, res.RequestID, res)
}

// requestHandlerV1 is a v1-compatible version of requestHandler
func restartHandlerV1(w http.ResponseWriter, r *http.Request) {
	timestamp := time.Now()
	ip := strings.Split(r.RemoteAddr, ":")[0]
	method := r.Method
	rid := randSeq()
	mainLogger := mainLog.WithFields(logrus.Fields{"request_id": rid})
	//bodyBytes, err := ioutil.ReadAll(r.Body)
	//if err != nil {
	//	Warnf("Could not read HTTP body!")
	//	respondWithError(w, http.StatusBadRequest, rid, "Invalid request payload")
	//}

	mainLogger.Debug("Incoming " + method + " request " + r.RequestURI + " from IP: " + ip)
	var request request
	var res response
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		//Warnf("Could not parse JSON request: " + string(bodyBytes))
		respondWithError(w, http.StatusBadRequest, rid, "Invalid request payload")
		return
	}
	defer r.Body.Close()

	if len(request.Fqdn) < 1 || len(request.Uptime) < 1 {
		respondWithError(w, http.StatusBadRequest, rid, "Invalid request payload. Need at least fqdn and uptime fields!")
		return
	}
	mainLogger.Debug("Received request with fqdn: " + request.Fqdn + " and uptime: " + string(request.Uptime))

	uptime, err := time.ParseDuration(request.Uptime)
	if err != nil {
		uptime_error := "Can not convert value " + request.Uptime + " of your uptime to a golang Duration. Valid time units are 300ms, 1.5h or 2h45m."
		Warnf("" + uptime_error)
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
		clusterLogger
		// TODO
		if !clusterSettings[c].Enabled {
			mainLogger.Debug("Skipping disabled Cluster setting " + c)
			continue
		}
		mainLogger.Debug("Checking against Cluster setting " + c)
		if regexp.MustCompile(clusterSettings[c].NamePattern).MatchString(request.Fqdn) {
			mainLogger.Debug("Found matching name pattern " + clusterSettings[c].NamePattern + " with fqdn from request " + request.Fqdn)
			res.UnknownHost = false
			res.FoundCluster = c
			res.Goahead = false

			fqdnBlacklisted := false
			for _, blacklistRegex := range clusterSettings[c].BlacklistNamePattern {
				if regexp.MustCompile(blacklistRegex).MatchString(request.Fqdn) {
					mainLogger.Debug("Found matching blacklist name pattern: " + blacklistRegex + " Preventing restart...")
					// make the client exit
					res.UnknownHost = true
					res.Message = "Found matching blacklist name pattern: " + blacklistRegex + " for FQDN: " + request.Fqdn + " Preventing restart!"
					fqdnBlacklisted = true
					break
					//respondWithJSON(w, http.StatusOK, rid, res)
					//return
				}
			}

			if fqdnBlacklisted {
				// check the next cluster setting if another cluster name pattern matches
				continue
			}

			if strings.HasPrefix(r.RequestURI, "/v1/inquire/") {
				res.Message = "No reason to restart"
				if uptime.Seconds() > clusterSettings[c].MinimumUptime.Seconds() {
					res.Message = "Your reported uptime " + time.Duration.String(uptime) + " is higher than the Configured minimum uptime " + time.Duration.String(clusterSettings[c].MinimumUptime) + " of the cluster " + c
				} else {
					inquireResult := checkAckFileInquire(request, res)
					if inquireResult.InquireToRestart {
						res.Message = inquireResult.Reason
					}
				}
				respondWithJSON(w, http.StatusOK, rid, res)
				return
			} else {

				if uptime.Seconds() < clusterSettings[c].MinimumUptime.Seconds() {
					res.AskagainIn = time.Duration.String(clusterSettings[c].MinimumUptime)
					mainLogger.Debug("Configured minimum uptime for cluster: " + time.Duration.String(clusterSettings[c].MinimumUptime) + " was not reached by client's uptime: " + request.Uptime)
					respondWithJSON(w, http.StatusOK, rid, res)
					return
				}

				res.AskagainIn = strconv.Itoa(rand.Intn(30)) + "s"
				result := checkAckFile(request, res)
				if result.FqdnGoAhead {
					result = checkClusterState(res, result)
				}
				if result.FqdnGoAhead && result.ClusterGoAhead {
					res.Goahead = true
					res.AskagainIn = "0s"
					select {
					case checkCluster <- clusterCheck{clusterSettings[c], request.Fqdn, rid, res.FoundCluster}:
						mainLogger.Debug("Activating cluster checker for " + request.Fqdn + " inside cluster " + res.FoundCluster)
					default:
					}
				} else {
					res.Message = result.Reason
				}
			}
			respondWithJSON(w, http.StatusOK, rid, res)
			return
		} else {
			mainLogger.Debug("Name pattern " + clusterSettings[c].NamePattern + " does not match with fqdn from request " + request.Fqdn)
			res.Message = "FQDN " + request.Fqdn + " did not match any known cluster"
		}
	}
	respondWithJSON(w, http.StatusOK, rid, res)

}

// AddV1Routes takes a router or subrouter and adds all the v1 routes to it
func addV1Routes(r *mux.Router) {
	r.HandleFunc("/request/restart/os", restartHandlerV1)
	r.HandleFunc("/inquire/restart/", restartHandlerV1)
	addRoutes(r)
}

// AddRoutes takes a router or subrouter and adds all the latest routes to it
func addRoutes(r *mux.Router) {
	r.HandleFunc("/", healthHandler)
	r.HandleFunc("/health", healthHandler)
	r.HandleFunc("/request/restart/", restartHandlerV1)
	r.HandleFunc("/inquire/restart/", restartHandlerV1)
}
