package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/kballard/go-shellquote"
	log "github.com/sirupsen/logrus"
)

// ExecResult contains the exit code and output of an external command (e.g. git)
type ExecResult struct {
	returnCode int
	output     string
}

// Debugf is a helper function for debug logging if global variable debug is set to true
func Debugf(s string) {
	if debug != false {
		pc, _, _, _ := runtime.Caller(1)
		callingFunctionName := strings.Split(runtime.FuncForPC(pc).Name(), ".")[len(strings.Split(runtime.FuncForPC(pc).Name(), "."))-1]
		if strings.HasPrefix(callingFunctionName, "func") {
			// check for anonymous function names
			log.Print("DEBUG " + fmt.Sprint(s))
		} else {
			log.Print("DEBUG " + callingFunctionName + "(): " + fmt.Sprint(s))
		}
	}
}

// Verbosef is a helper function for verbose logging if global variable verbose is set to true
func Verbosef(s string) {
	if debug != false || verbose != false {
		log.Print(fmt.Sprint(s))
	}
}

// Infof is a helper function for info logging if global variable info is set to true
func Infof(s string) {
	if debug != false || verbose != false || info != false {
		color.Green(s)
	}
}

// Warnf is a helper function for warning logging
func Warnf(s string) {
	pc, _, _, _ := runtime.Caller(1)
	callingFunctionName := strings.Split(runtime.FuncForPC(pc).Name(), ".")[len(strings.Split(runtime.FuncForPC(pc).Name(), "."))-1]
	color.Set(color.FgYellow)
	log.Print("WARN " + callingFunctionName + "(): " + fmt.Sprint(s))
	color.Unset()
}

// Fatalf is a helper function for fatal logging
func Fatalf(s string) {
	color.New(color.FgRed).Fprintln(os.Stderr, s)
	os.Exit(1)
}

// fileExists checks if the given file exists and returns a bool
func fileExists(file string) bool {
	//Debugf("checking for file existence " + file)
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return false
	}
	return true
}

// isDir checks if the given dir exists and returns a bool
func isDir(dir string) bool {
	fi, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return false
	}
	if fi.Mode().IsDir() {
		return true
	}
	return false
}

// normalizeDir removes from the given directory path multiple redundant slashes and adds a trailing slash
func normalizeDir(dir string) string {
	if strings.Count(dir, "//") > 0 {
		dir = normalizeDir(strings.Replace(dir, "//", "/", -1))
	} else {
		if !strings.HasSuffix(dir, "/") {
			dir = dir + "/"
		}
	}
	return dir
}

// checkDirAndCreate tests if the given directory exists and tries to create it
func checkDirAndCreate(dir string, name string) string {
	if len(dir) != 0 {
		if !fileExists(dir) {
			//log.Printf("checkDirAndCreate(): trying to create dir '%s' as %s", dir, name){
			if err := os.MkdirAll(dir, 0777); err != nil {
				Fatalf("checkDirAndCreate(): Error: failed to create directory: " + dir)
			}
		} else {
			if !isDir(dir) {
				Fatalf("checkDirAndCreate(): Error: " + dir + " exists, but is not a directory! Exiting!")
			}
		}
	} else {
		// TODO make dir optional
		Fatalf("checkDirAndCreate(): Error: dir setting '" + name + "' missing! Exiting!")
	}
	dir = normalizeDir(dir)
	return dir
}

func createOrPurgeDir(dir string, callingFunction string) {
	if !fileExists(dir) {
		Debugf("Trying to create dir: " + dir + " called from " + callingFunction)
		os.MkdirAll(dir, 0777)
	} else {
		Debugf("Trying to remove: " + dir + " called from " + callingFunction)
		if err := os.RemoveAll(dir); err != nil {
			log.Print("createOrPurgeDir(): error: removing dir failed", err)
		}
		Debugf("Trying to create dir: " + dir + " called from " + callingFunction)
		os.MkdirAll(dir, 0777)
	}
}

func purgeDir(dir string, callingFunction string) {
	if fileExists(dir) {
		if err := os.RemoveAll(dir); err != nil {
			log.Print("purgeDir(): os.RemoveAll() error: removing dir failed: ", err)
			if err = syscall.Unlink(dir); err != nil {
				log.Print("purgeDir(): syscall.Unlink() error: removing link failed: ", err)
			}
		}
	}
}

func executeCommand(command string, timeout int, allowFail bool, logger *log.Entry) ExecResult {
	logger.Info("Executing " + command)
	parts := strings.SplitN(command, " ", 2)
	cmd := parts[0]
	cmdArgs := []string{}
	if len(parts) > 1 {
		args, err := shellquote.Split(parts[1])
		if err != nil {
			logger.Warn("err: " + fmt.Sprint(err))
		} else {
			cmdArgs = args
		}
	}

	before := time.Now()
	out, err := exec.Command(cmd, cmdArgs...).CombinedOutput()
	duration := time.Since(before).Seconds()
	er := ExecResult{0, string(out)}
	if msg, ok := err.(*exec.ExitError); ok { // there is error code
		er.returnCode = msg.Sys().(syscall.WaitStatus).ExitStatus()
	}
	logger.Debug("Executing " + command + " took " + strconv.FormatFloat(duration, 'f', 5, 64) + "s")
	if err != nil {
		if !allowFail {
			logger.Warn("executeCommand(): command failed: " + command + " " + err.Error() + "\nOutput: " + string(out))
		} else {
			er.returnCode = 1
			er.output = fmt.Sprint(err)
		}
	}
	return er
}

// funcName return the function name as a string
func funcName() string {
	pc, _, _, _ := runtime.Caller(1)
	completeFuncname := runtime.FuncForPC(pc).Name()
	return strings.Split(completeFuncname, ".")[len(strings.Split(completeFuncname, "."))-1]
}

func timeTrack(start time.Time, name string) {
	duration := time.Since(start).Seconds()
	Debugf(name + "() took " + strconv.FormatFloat(duration, 'f', 5, 64) + "s")
}

// getSha256sumFile return the SHA256 hash sum of the given file
func getSha256sumFile(file string) string {
	// https://golang.org/pkg/crypto/sha256/#New
	f, err := os.Open(file)
	if err != nil {
		Fatalf("failed to open file " + file + " to calculate SHA256 sum. Error: " + err.Error())
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		Fatalf("failed to calculate SHA256 sum of file " + file + " Error: " + err.Error())
	}

	return string(h.Sum(nil))
}

// randSeq returns a fixed length random string to identify each request in the log
// http://stackoverflow.com/a/22892986/682847
func randSeq() string {
	b := make([]rune, 8)
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	rand.Seed(time.Now().UTC().UnixNano())
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func writeStructJSONFile(file string, v interface{}) {
	f, err := os.Create(file)
	if err != nil {
		Warnf("Could not write JSON file " + file + " " + err.Error())
	}
	defer f.Close()
	json, err := json.Marshal(v)
	if err != nil {
		Warnf("Could not encode JSON file " + file + " " + err.Error())
	}
	f.Write(json)
}

func readClusterStateFile(file string, cluster string, clusterLogger *log.Entry) clusterState {
	clusterLogger.Debug("Trying to read json file: " + file)
	data, err := ioutil.ReadFile(file)
	if err != nil {
		clusterLogger.Warn("readStructJSONFile(): There was an error parsing the json file " + file + ": " + err.Error())
	}

	var cs clusterState
	err = json.Unmarshal([]byte(data), &cs)
	if err != nil {
		clusterLogger.Warn("In json file " + file + ": JSON unmarshal error: " + err.Error())
	}
	return cs
}

func readAckFile(file string, res response, cluster string, clusterLogger *log.Entry) response {
	clusterLogger.Debug("Trying to read json file: " + file)
	data, err := ioutil.ReadFile(file)
	if err != nil {
		clusterLogger.Warn("readStructJSONFile(): There was an error parsing the json file " + file + ": " + err.Error())
	}

	err = json.Unmarshal([]byte(data), &res)
	if err != nil {
		clusterLogger.Warn("In json file " + file + ": JSON unmarshal error: " + err.Error())
	}
	return res
}

func keysString(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func initLogger(fileName string) *log.Entry {
	log.Debug("setting up log file:" + filepath.Join(config.LogBaseDir, fileName+".log"))
	var logrusLog = log.New()
	file, err := os.OpenFile(filepath.Join(config.LogBaseDir, fileName+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		logrusLog.Out = file
	} else {
		log.Fatal("Failed to log to file " + filepath.Join(config.LogBaseDir, fileName+".log") + " Error: " + err.Error())
	}
	if debug {
		logrusLog.SetLevel(log.DebugLevel)
	}
	logger := logrusLog.WithFields(log.Fields{})
	return logger
}

func compareDurationString(a string, b string) string {
	da, err := time.ParseDuration(a)
	if err != nil {
		Warnf("Can not convert value " + a + " of your uptime to a golang Duration. Valid time units are 300ms, 1.5h or 2h45m.")
	}
	db, err := time.ParseDuration(b)
	if err != nil {
		Warnf("Can not convert value " + b + " of your uptime to a golang Duration. Valid time units are 300ms, 1.5h or 2h45m.")
	}

	if da.Seconds() < db.Seconds() {
		return "shorter"
	}
	return "longer"
}
