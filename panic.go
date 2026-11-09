package main

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/sirupsen/logrus"
)

const (
	// panicTimerInterval is how often the background timer looks for clusters that exceeded reboot_completion_panic_threshold
	panicTimerInterval = 30 * time.Second
	// panicCommandTimeout limits every single check or script executed while handling a reboot panic
	panicCommandTimeout = 120 * time.Second
	// panicMaxOutputBytes caps the captured output of each command in the mail
	panicMaxOutputBytes = 256 * 1024
)

// panicExec is the full record of one command execution that ends up in the panic mail
type panicExec struct {
	Command  string
	ExitCode int
	Output   string
	Duration time.Duration
	Err      string
}

// panicEvent describes one reboot completion panic
type panicEvent struct {
	Cluster            string
	Hosts              []string
	Trigger            string
	LastRestartRequest time.Time
}

// rebootPanicDue reports if the restarting servers of a cluster exceeded the configured reboot_completion_panic_threshold.
// The caller must hold the global mutex.
func rebootPanicDue(cluster string, cs clusterState) bool {
	threshold := clusterSettings[cluster].RebootCompletionPanicThreshold
	return threshold > 0 &&
		len(cs.CurrentRestartingServers) > 0 &&
		// we do not want to run the actions on every check, we wait for the next threshold (acts like an interval)
		time.Since(cs.LastRestartPanicTimestamp) > threshold &&
		// we have met the threshold
		time.Since(cs.LastRestartRequestTimestamp) > threshold
}

// startRebootPanicTimer periodically checks all clusters for hosts that did not come back, so the panic actions do not depend on incoming requests
func startRebootPanicTimer() {
	ticker := time.NewTicker(panicTimerInterval)
	defer ticker.Stop()
	for range ticker.C {
		for cluster := range clusterSettings {
			checkRebootPanicTimer(cluster)
		}
	}
}

func checkRebootPanicTimer(cluster string) {
	// disabled clusters (also the automatically disabled ones) must not send anything, like the request path
	if !clusterSettings[cluster].Enabled || clusterSettings[cluster].RebootCompletionPanicThreshold <= 0 {
		return
	}
	clusterFile := filepath.Join(config.SaveStateDir, cluster+".json")
	clusterLogger := clusterLoggers[cluster]

	mutex.Lock()
	if !fileExists(clusterFile) {
		mutex.Unlock()
		return
	}
	cs := readClusterStateFile(clusterFile, cluster, clusterLogger)
	if !rebootPanicDue(cluster, cs) {
		mutex.Unlock()
		return
	}
	cs.LastRestartPanicTimestamp = time.Now()
	if err := writeStructJSONFile(clusterFile, cs); err != nil {
		clusterLogger.Error("Could not save cluster state file on restart panic timer: " + clusterFile + " " + err.Error())
	}
	event := panicEvent{Cluster: cluster, Hosts: keysString(cs.CurrentRestartingServers), Trigger: "timer", LastRestartRequest: cs.LastRestartRequestTimestamp}
	mutex.Unlock()

	clusterLogger.Info("Reboot panic timer fired for cluster " + cluster + " because " + strings.Join(event.Hosts, ",") + " still offline!")
	go handleRebootPanic(event, clusterLogger)
}

// substitutePlaceholders replaces the {:%...%:} placeholders of configured commands
func substitutePlaceholders(s, fqdn, cluster, uptime string) string {
	s = strings.Replace(s, "{:%fqdn%:}", fqdn, -1)
	s = strings.Replace(s, "{:%cluster%:}", cluster, -1)
	s = strings.Replace(s, "{:%uptime%:}", uptime, -1)
	return strings.Replace(s, "{:%hostname%:}", strings.SplitN(fqdn, ".", 2)[0], -1)
}

// runCaptured executes a command with a timeout and keeps the real exit code and output (executeCommand replaces them on failure)
func runCaptured(command string, timeout time.Duration) panicExec {
	pe := panicExec{Command: command}
	args, err := shellquote.Split(command)
	if err != nil || len(args) == 0 {
		pe.ExitCode = -1
		pe.Err = fmt.Sprintf("could not parse command: %v", err)
		return pe
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	before := time.Now()
	err = cmd.Run()
	pe.Duration = time.Since(before)
	pe.Output = buf.String()
	if len(pe.Output) > panicMaxOutputBytes {
		pe.Output = pe.Output[:panicMaxOutputBytes] + "\n[output truncated]"
	}
	if err != nil {
		pe.ExitCode = -1
		if ee, ok := err.(*exec.ExitError); ok {
			pe.ExitCode = ee.ExitCode()
		}
		pe.Err = err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			pe.Err = "timed out after " + timeout.String() + ": " + pe.Err
		}
	}
	return pe
}

// handleRebootPanic runs the configured scripts, re-runs the reboot completion check for every host that is still offline and mails the full result
func handleRebootPanic(event panicEvent, clusterLogger *logrus.Entry) {
	cs := clusterSettings[event.Cluster]
	sort.Strings(event.Hosts)

	var body strings.Builder
	fmt.Fprintf(&body, "Host(s) did not come back after the reboot goahead in cluster %s.\n\n", event.Cluster)
	fmt.Fprintf(&body, "Cluster:                  %s\n", event.Cluster)
	fmt.Fprintf(&body, "Still offline:            %s\n", strings.Join(event.Hosts, ", "))
	fmt.Fprintf(&body, "Panic threshold:          %s\n", cs.RebootCompletionPanicThreshold)
	if !event.LastRestartRequest.IsZero() {
		fmt.Fprintf(&body, "Last goahead given:       %s (%s ago)\n", event.LastRestartRequest.Format(time.RFC3339), time.Since(event.LastRestartRequest).Round(time.Second))
	}
	fmt.Fprintf(&body, "Triggered by:             %s\n", event.Trigger)
	fmt.Fprintf(&body, "Completion check:         %s\n", cs.RebootCompletionCheck)
	fmt.Fprintf(&body, "Required successes:       %d\n", cs.RebootCompletionCheckConsecutiveSuccesses)
	if url := clusterConfigURL(cs); url != "" {
		fmt.Fprintf(&body, "Cluster config:           %s\n", url)
	}
	hostname, _ := os.Hostname()
	fmt.Fprintf(&body, "Reported by goahead on:   %s at %s\n", hostname, time.Now().Format(time.RFC3339))

	for _, host := range event.Hosts {
		uptime := "unknown"
		ackFile := filepath.Join(config.SaveStateDir, event.Cluster, host+".json")
		if fileExists(ackFile) {
			if ack := readAckFile(ackFile, response{}, event.Cluster, clusterLogger); ack.ReportedUptime != "" {
				uptime = ack.ReportedUptime
			}
		}
		fmt.Fprintf(&body, "\n==================== %s (last reported uptime: %s) ====================\n", host, uptime)

		if cs.RebootCompletionCheck != "" {
			body.WriteString("\n--- reboot completion check, executed again now ---\n")
			pe := runCaptured(substitutePlaceholders(cs.RebootCompletionCheck, host, event.Cluster, uptime), panicCommandTimeout)
			clusterLogger.Info("reboot completion panic check result of " + pe.Command + " is " + fmt.Sprint(pe.ExitCode))
			body.WriteString(formatPanicExec(pe))
			if pe.ExitCode == 0 {
				body.WriteString("NOTE: the check succeeded now, the host might have come back in the meantime.\n")
			}
		}

		for _, action := range cs.RebootCompletionPanicActions.Scripts {
			clusterLogger.Info("found reboot completion panic action:" + action)
			body.WriteString("\n--- panic action ---\n")
			pe := runCaptured(substitutePlaceholders(action, host, event.Cluster, uptime), panicCommandTimeout)
			clusterLogger.Info("reboot completion panic action result of " + pe.Command + " is " + fmt.Sprint(pe.ExitCode))
			body.WriteString(formatPanicExec(pe))
		}
	}

	if len(cs.RebootCompletionPanicActions.Mail) == 0 {
		return
	}
	subject := fmt.Sprintf("[goahead] PANIC: %s did not come back in cluster %s after %s", strings.Join(event.Hosts, ","), event.Cluster, cs.RebootCompletionPanicThreshold)
	if err := sendMail(cs.RebootCompletionPanicActions.Mail, subject, body.String(), clusterLogger); err != nil {
		clusterLogger.Error("Could not send reboot completion panic mail: " + err.Error())
	}
}

func formatPanicExec(pe panicExec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Command:   %s\n", pe.Command)
	fmt.Fprintf(&b, "Exit code: %d\n", pe.ExitCode)
	fmt.Fprintf(&b, "Duration:  %s\n", pe.Duration.Round(time.Millisecond))
	if pe.Err != "" {
		fmt.Fprintf(&b, "Error:     %s\n", pe.Err)
	}
	fmt.Fprintf(&b, "Output:\n%s\n", strings.TrimRight(pe.Output, "\n"))
	return b.String()
}

// clusterConfigURL builds the link to the cluster settings file in the repository, if cluster_config_url_base is set
func clusterConfigURL(cs clusterSetting) string {
	if config.ClusterConfigURLBase == "" || cs.ConfigFile == "" {
		return ""
	}
	rel, err := filepath.Rel(config.IncludeDir, cs.ConfigFile)
	if err != nil {
		return ""
	}
	return strings.TrimRight(config.ClusterConfigURLBase, "/") + "/" + filepath.ToSlash(rel)
}

// sendMail hands the message to the local sendmail binary
func sendMail(to []string, subject, body string, logger *logrus.Entry) error {
	hostname, _ := os.Hostname()
	from := config.MailFrom
	if from == "" {
		from = "goahead@" + hostname
	}
	var recipients []string
	for _, addr := range to {
		addr = strings.TrimSpace(addr)
		if addr == "" || strings.ContainsAny(addr, "\r\n,<>") {
			logger.Warn("Skipping invalid mail address: " + fmt.Sprintf("%q", addr))
			continue
		}
		recipients = append(recipients, addr)
	}
	if len(recipients) == 0 {
		return fmt.Errorf("no valid mail recipients")
	}
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: goahead <%s>\n", clean(from))
	fmt.Fprintf(&msg, "To: %s\n", strings.Join(recipients, ", "))
	fmt.Fprintf(&msg, "Subject: %s\n", mime.QEncoding.Encode("utf-8", clean(subject)))
	fmt.Fprintf(&msg, "Date: %s\n", time.Now().Format(time.RFC1123Z))
	msg.WriteString("MIME-Version: 1.0\nContent-Type: text/plain; charset=UTF-8\nContent-Transfer-Encoding: 8bit\n\n")
	msg.WriteString(body)

	sendmail := config.SendmailPath
	if sendmail == "" {
		sendmail = "/usr/sbin/sendmail"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, sendmail, append([]string{"-i", "--"}, recipients...)...)
	cmd.Stdin = &msg
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s failed: %v: %s", sendmail, err, strings.TrimSpace(string(out)))
	}
	logger.Info("Sent reboot completion panic mail to " + strings.Join(recipients, ","))
	return nil
}
