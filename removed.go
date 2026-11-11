package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// removedHostsTimerInterval is how often restarting hosts are checked for being deleted
	removedHostsTimerInterval = 5 * time.Minute
	// defaultRemovedHostGrace is used when reboot_completion_removed_host_grace is not set
	defaultRemovedHostGrace = time.Hour
	// presenceLookupTimeout limits one DNS query
	presenceLookupTimeout = 5 * time.Second
	// removedHostCheckTimeout limits one reboot_completion_removed_host_check command
	removedHostCheckTimeout = 30 * time.Second
)

type hostPresence int

const (
	hostUnknown hostPresence = iota
	hostPresent
	hostGone
)

func (p hostPresence) String() string {
	switch p {
	case hostPresent:
		return "exists"
	case hostGone:
		return "does NOT exist"
	}
	return "unknown"
}

// presenceResult is the verdict of one lookup. Only a definite answer is present or gone, every error is unknown and never releases a host.
type presenceResult struct {
	Presence hostPresence
	Detail   string
}

// lookup functions are variables so the tests can replace them
var (
	hookLookup = runRemovedHostCheck
	dnsLookup  = lookupDNS
	// goneSince remembers since when a host was continuously seen as removed, key is cluster + "/" + fqdn
	goneSince   = make(map[string]time.Time)
	goneSinceMu sync.Mutex
)

// runRemovedHostCheck runs the configured reboot_completion_removed_host_check command: exit code 0 means the host was removed, 1 means it still exists, everything else (including a timeout) is unknown
func runRemovedHostCheck(command, fqdn, cluster string) presenceResult {
	pe := runCaptured(substitutePlaceholders(command, fqdn, cluster, ""), removedHostCheckTimeout)
	out := strings.Join(strings.Fields(pe.Output), " ")
	detail := fmt.Sprintf("removed_host_check %q exit code %d: %s", pe.Command, pe.ExitCode, out)
	switch pe.ExitCode {
	case 0:
		return presenceResult{hostGone, detail}
	case 1:
		return presenceResult{hostPresent, detail}
	}
	return presenceResult{hostUnknown, detail}
}

// lookupDNS reports gone only for a definite NXDOMAIN, SERVFAIL and timeouts are unknown
func lookupDNS(fqdn string) presenceResult {
	ctx, cancel := context.WithTimeout(context.Background(), presenceLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, fqdn)
	if err == nil {
		return presenceResult{hostPresent, "DNS resolves to " + strings.Join(addrs, ", ")}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return presenceResult{hostGone, "DNS NXDOMAIN: " + err.Error()}
	}
	return presenceResult{hostUnknown, "DNS lookup failed: " + err.Error()}
}

// hostRemoved combines the lookups. If the cluster has a reboot_completion_removed_host_check, that command is authoritative: a host it reports as existing is never removed, even if DNS says NXDOMAIN. If the command can not decide (or is not set), a definite DNS NXDOMAIN counts.
func hostRemoved(cluster, fqdn string) presenceResult {
	var hookDetail string
	if cmd := clusterSettings[cluster].RemovedHostCheck; cmd != "" {
		h := hookLookup(cmd, fqdn, cluster)
		if h.Presence != hostUnknown {
			return h
		}
		hookDetail = h.Detail + "; "
	}
	d := dnsLookup(fqdn)
	d.Detail = hookDetail + d.Detail
	return d
}

// describeHostPresence is the human readable status of both lookups for the panic mail
func describeHostPresence(cluster, fqdn string) string {
	var b strings.Builder
	if cmd := clusterSettings[cluster].RemovedHostCheck; cmd != "" {
		h := hookLookup(cmd, fqdn, cluster)
		fmt.Fprintf(&b, "Host check:   %s (%s)\n", h.Presence, h.Detail)
	}
	d := dnsLookup(fqdn)
	fmt.Fprintf(&b, "DNS:          %s (%s)\n", d.Presence, d.Detail)
	return b.String()
}

// removeHostFromState removes fqdn from the cluster state and reports if it was in there
func removeHostFromState(cs *clusterState, fqdn string) bool {
	if _, ok := cs.CurrentRestartingServers[fqdn]; !ok {
		return false
	}
	delete(cs.CurrentRestartingServers, fqdn)
	cs.CurrentOngoingRestarts--
	if cs.CurrentOngoingRestarts < 0 {
		cs.CurrentOngoingRestarts = 0
	}
	return true
}

// hostStillRestarting tells the completion check loop if its host is still part of the cluster state, it stops polling for released or forgotten hosts
func hostStillRestarting(cluster, fqdn string, clusterLogger *logrus.Entry) bool {
	clusterFile := filepath.Join(config.SaveStateDir, cluster+".json")
	mutex.Lock()
	defer mutex.Unlock()
	if !fileExists(clusterFile) {
		return false
	}
	cs := readClusterStateFile(clusterFile, cluster, clusterLogger)
	_, ok := cs.CurrentRestartingServers[fqdn]
	return ok
}

// startRemovedHostsTimer periodically looks for restarting hosts that were deleted
func startRemovedHostsTimer() {
	ticker := time.NewTicker(removedHostsTimerInterval)
	defer ticker.Stop()
	for range ticker.C {
		for cluster := range clusterSettings {
			checkRemovedHosts(cluster)
		}
	}
}

// checkRemovedHosts releases the hosts of a cluster that are in the cluster state but were seen as removed for reboot_completion_removed_host_grace
func checkRemovedHosts(cluster string) {
	cset := clusterSettings[cluster]
	if !cset.Enabled || !cset.ReleaseRemovedHosts {
		return
	}
	clusterLogger := clusterLoggers[cluster]
	clusterFile := filepath.Join(config.SaveStateDir, cluster+".json")
	mutex.Lock()
	var hosts []string
	if fileExists(clusterFile) {
		hosts = keysString(readClusterStateFile(clusterFile, cluster, clusterLogger).CurrentRestartingServers)
	}
	mutex.Unlock()

	grace := cset.RemovedHostGrace
	if grace <= 0 {
		grace = defaultRemovedHostGrace
	}
	for _, fqdn := range hosts {
		key := cluster + "/" + fqdn
		r := hostRemoved(cluster, fqdn)
		goneSinceMu.Lock()
		if r.Presence != hostGone {
			delete(goneSince, key)
			goneSinceMu.Unlock()
			continue
		}
		since, seen := goneSince[key]
		if !seen {
			since = time.Now()
			goneSince[key] = since
			clusterLogger.Warn("Restarting host " + fqdn + " in cluster " + cluster + " seems to be removed: " + r.Detail + ". Releasing it after " + grace.String() + " if that stays true")
		}
		goneSinceMu.Unlock()
		if time.Since(since) >= grace {
			releaseRemovedHost(cluster, fqdn, r.Detail, since, clusterLogger)
			goneSinceMu.Lock()
			delete(goneSince, key)
			goneSinceMu.Unlock()
		}
	}
}

// releaseRemovedHost takes a deleted host out of the cluster state so it does not block the cluster anymore, and tells the panic mail recipients
func releaseRemovedHost(cluster, fqdn, detail string, since time.Time, clusterLogger *logrus.Entry) {
	clusterFile := filepath.Join(config.SaveStateDir, cluster+".json")
	mutex.Lock()
	delete(sleepingClusterChecks, fqdn)
	released := false
	if fileExists(clusterFile) {
		cs := readClusterStateFile(clusterFile, cluster, clusterLogger)
		if released = removeHostFromState(&cs, fqdn); released {
			if err := writeStructJSONFile(clusterFile, cs); err != nil {
				clusterLogger.Error("Could not save cluster state file " + clusterFile + ": " + err.Error())
			}
		}
	}
	mutex.Unlock()
	if !released {
		return
	}
	deleteAckFile(fqdn, cluster)
	msg := "Released removed host " + fqdn + " from the restarting hosts of cluster " + cluster + ": " + detail + " (seen as removed since " + since.Format(time.RFC3339) + ")"
	clusterLogger.Warn(msg)
	if mail := clusterSettings[cluster].RebootCompletionPanicActions.Mail; len(mail) > 0 {
		body := msg + "\n\nThe host was restarting, but it does not exist anymore, so it can not come back. goahead stopped waiting for it, the other hosts of the cluster can restart again.\n"
		if err := sendMail(mail, "[goahead] released removed host "+fqdn+" from cluster "+cluster, body, clusterLogger); err != nil {
			clusterLogger.Error("Could not send release mail: " + err.Error())
		}
	}
}

// forgetHost removes fqdn from the cluster state (all clusters if cluster is empty) and deletes its ACK file. It works on the state files, so it can run next to a running server: the completion check of the host notices it is gone and stops.
func forgetHost(fqdn, cluster string, logger *logrus.Entry) bool {
	var files []string
	if cluster != "" {
		files = []string{filepath.Join(config.SaveStateDir, cluster+".json")}
	} else {
		files, _ = filepath.Glob(filepath.Join(config.SaveStateDir, "*.json"))
	}
	found := false
	for _, f := range files {
		if !fileExists(f) {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".json")
		cs := readClusterStateFile(f, name, logger)
		if !removeHostFromState(&cs, fqdn) {
			continue
		}
		if err := writeStructJSONFile(f, cs); err != nil {
			logger.Error("Could not save cluster state file " + f + ": " + err.Error())
			continue
		}
		deleteAckFile(fqdn, name)
		fmt.Printf("Removed %s from cluster state of %s (%d restart(s) still ongoing)\n", fqdn, name, cs.CurrentOngoingRestarts)
		found = true
	}
	return found
}
