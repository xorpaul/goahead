package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	defaultURL = "https://127.0.0.1:8443/"
)

func prepareHTTPClient(t *testing.T) *http.Client {
	// Get the SystemCertPool, continue with an empty pool on error
	rootCAs, _ := x509.SystemCertPool()
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}

	if len(config.ClientCertCaFile) > 0 {
		// Read in the cert file
		certs, err := os.ReadFile(config.ClientCertCaFile)
		if err != nil {
			t.Error("Failed to append " + config.ClientCertCaFile + " to RootCAs Error: " + err.Error())
		}

		// Append our cert to the system pool
		if ok := rootCAs.AppendCertsFromPEM(certs); !ok {
		}

	}

	// Trust the augmented cert pool in our client
	tlsConfig := &tls.Config{
		RootCAs: rootCAs,
	}
	tr := &http.Transport{TLSClientConfig: tlsConfig}
	return &http.Client{Transport: tr}
}

func doRequest(req request, uri string, t *testing.T) response {
	client := prepareHTTPClient(t)
	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Error("Error while json.Marshal request. Error: " + err.Error())
	}
	Debugf("Sending request body: " + string(reqBytes))

	resp, err := client.Post(defaultURL+uri, "application/json", bytes.NewBuffer(reqBytes))
	if err != nil {
		t.Error("Error while issuing request to " + defaultURL + " Error: " + err.Error())
	}
	defer resp.Body.Close()

	body, err := os.ReadAll(resp.Body)
	if err != nil {
		t.Error("Error while reading response body: " + err.Error())
	}

	Debugf("Recieved response body: " + string(body))
	var response response
	err = json.Unmarshal(body, &response)
	if err != nil {
		t.Error("Could not parse JSON response: " + string(body) + " Error: " + err.Error())
	}

	return response
}

func TestMain(m *testing.M) {
	purgeDir("/tmp/goahead/", "TestMain")
	go main()
	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestUnknownServer(t *testing.T) {
	config.SaveStateDir = "/tmp/goahead/" + funcName()
	config.LogBaseDir = config.SaveStateDir
	req := request{Fqdn: "unknown.domain.tld", Uptime: "2h31m"}
	resp := doRequest(req, "v1/request/restart/os", t)
	if resp.UnknownHost != true {
		t.Error("Did not receive unknown_host: true")
	}
}

func TestKnownServer(t *testing.T) {
	config.SaveStateDir = "/tmp/goahead/" + funcName()
	config.LogBaseDir = config.SaveStateDir
	req := request{Fqdn: "foobar-server-aa07.domain.tld", Uptime: "2h31m"}
	resp := doRequest(req, "v1/request/restart/os", t)
	if resp.FoundCluster != "foobar-server" {
		t.Error("Did not receive found_cluster: 'foobar-server'")
	}
}

func TestMimimumUptime(t *testing.T) {
	config.SaveStateDir = "/tmp/goahead/" + funcName()
	config.LogBaseDir = config.SaveStateDir
	expectedLine := "Configured minimum uptime for cluster: 30m0s was not reached by client's uptime: 1m"
	req := request{Fqdn: "foobar-server-aa07.domain.tld", Uptime: "1m"}
	resp := doRequest(req, "v1/request/restart/os", t)
	if !strings.Contains(string(resp.Message), expectedLine) {
		t.Errorf("Could not find expected line '" + expectedLine + "' in response message field: " + resp.Message)
	}
}

func TestGoahead(t *testing.T) {
	config.SaveStateDir = "/tmp/goahead/" + funcName()
	config.LogBaseDir = config.SaveStateDir
	req := request{Fqdn: "foobar-server-aa07.domain.tld", Uptime: "2h31m"}
	resp := doRequest(req, "v1/request/restart/os", t)
	if len(resp.RequestID) < 1 {
		t.Error("Did not receive any request_id in response")
	}

	if resp.Message != "No previous request file found for fqdn: foobar-server-aa07.domain.tld" {
		t.Error("Did not receive expected message field!")
	}

	req.RequestID = resp.RequestID
	resp = doRequest(req, "v1/request/restart/os", t)
	if resp.Goahead != true {
		t.Error("Did not receive go_ahead: true")
	}

	expectedLines := []string{
		"Executing ./tests/goahead_action.sh foobar-server-aa07.domain.tld foobar-server",
	}
	foobarServerLogfile := "/tmp/goahead/foobar-server.log"
	content, _ := os.ReadFile(foobarServerLogfile)

	for _, expectedLine := range expectedLines {
		if !strings.Contains(string(content), expectedLine) {
			t.Errorf("Could not find expected line '" + expectedLine + "' in goahead cluster logfile " + foobarServerLogfile + ". Check variable replacement in reboot_goahead_actions command.")
		}
	}

	req = request{Fqdn: "foobar-server-aa07.domain.tld", Uptime: "2h31m"}
	resp = doRequest(req, "v1/request/restart/os", t)
	req.RequestID = resp.RequestID
	resp = doRequest(req, "v1/request/restart/os", t)
	if resp.Goahead != false || resp.Message != "You should already be restarting!" {
		t.Error("Unexpected response for already restarting server")
	}

	resp = doRequest(req, "v1/inquire/restart/", t)
	if resp.Goahead != false || resp.Message != "No reason to restart" {
		fmt.Println(resp.Message)
		t.Error("Unexpected response for inquire request with high uptime")
	}
	req.Uptime = "2m"
	req.Fqdn = "foobar-server-aa99.domain.tld"
	resp = doRequest(req, "v1/inquire/restart/", t)

	//expectedLines = []string{
	//	"Received inquire request from FQDN foobar-server-aa07.domain.tld Did not match sleeping check for FQDN: foobar-server-aa99.domain.tld",
	//}
	//for _, expectedLine := range expectedLines {
	//	if !strings.Contains(string(contentChecker), expectedLine) {
	//		t.Errorf("Could not find expected line '" + expectedLine + "' in goahead checker logfile " + checkerLogfile)
	//	}
	//}

	req.Fqdn = "foobar-server-aa07.domain.tld"
	req.Uptime = "99999h"
	resp = doRequest(req, "v1/inquire/restart/", t)
	expectedLines = []string{
		"Received inquire request from FQDN foobar-server-aa07.domain.tld Interrupting reboot_completion_check_offset sleep!",
	}
	checkerLogfile := "/tmp/goahead/checker.log"
	contentChecker, _ := os.ReadFile(checkerLogfile)
	for _, expectedLine := range expectedLines {
		if strings.Contains(string(contentChecker), expectedLine) {
			t.Errorf("Did find line '" + expectedLine + "' in goahead checker logfile " + checkerLogfile + ", but it should not be here, because the reported_uptime wasn't lower than the previuously received uptime. Indicating that no reboot happened!")
		}
	}

	req.Uptime = "2s"
	resp = doRequest(req, "v1/inquire/restart/", t)
	expectedLines = []string{
		"Received inquire request from FQDN foobar-server-aa07.domain.tld Interrupting reboot_completion_check_offset sleep!",
	}
	contentChecker, _ = os.ReadFile(checkerLogfile)
	for _, expectedLine := range expectedLines {
		if !strings.Contains(string(contentChecker), expectedLine) {
			t.Errorf("Did not find expected line '" + expectedLine + "' in goahead checker logfile " + checkerLogfile)
		}
	}

	if fileExists(filepath.Join(config.SaveStateDir, resp.FoundCluster, req.Fqdn)) {
		t.Errorf("FQDN state file still exists, but should not after successfull reboot and checks!")
	}
	rebootCompletionFile := filepath.Join("/tmp/goahead", resp.FoundCluster+"-"+req.Fqdn+"-successful-reboot")
	time.Sleep(1 * time.Second)
	if !fileExists(rebootCompletionFile) {
		t.Errorf("Reboot completion action trigger created file does not exist: %s", rebootCompletionFile)
	}
}

func testGoaheadFalse(t *testing.T) {
	debug = true
	config.SaveStateDir = "/tmp/goahead/" + funcName()
	config.LogBaseDir = config.SaveStateDir
	req := request{Fqdn: "foobar-server-aa07.domain.tld", Uptime: "2h31m"}
	resp := doRequest(req, "v1/request/restart/os", t)
	if len(resp.RequestID) > 1 {
		t.Error("Did not receive any request_id in response")
	}

	req.RequestID = resp.RequestID
	resp = doRequest(req, "v1/request/restart/os", t)
	if resp.Goahead != true {
		t.Error("First server " + req.Fqdn + " did not receive go_ahead: true")
	}

	req = request{Fqdn: "foobar-server-aa67.domain.tld", Uptime: "8h59m"}
	resp = doRequest(req, "v1/request/restart/os", t)
	if len(resp.RequestID) < 1 {
		t.Error("Did not receive any request_id in response")
	}

	req.RequestID = resp.RequestID
	resp = doRequest(req, "v1/request/restart/os", t)
	if resp.Goahead != true {
		t.Error("Second server" + req.Fqdn + "did not receive go_ahead: true")
	}

	req = request{Fqdn: "foobar-server-aa90.domain.tld", Uptime: "8h59m"}
	resp = doRequest(req, "v1/request/restart/os", t)
	if len(resp.RequestID) < 1 {
		t.Error("Did not receive any request_id in response")
	}

	req.RequestID = resp.RequestID
	resp = doRequest(req, "v1/request/restart/os", t)
	if resp.Goahead != false {
		t.Error("Third server" + req.Fqdn + "did not receive go_ahead: false")
	}
	expectedLine := "Denied restart request as the current_ongoing_restarts of cluster foobar-server is larger than the allowed_parallel_restarts: 2 >= 2 Currently restarting hosts: foobar-server-aa07.domain.tld,foobar-server-aa67.domain.tld"
	if !strings.Contains(string(resp.Message), expectedLine) {
		t.Errorf("Could not find expected line '" + expectedLine + "' in response message field: " + resp.Message)
	}

}
