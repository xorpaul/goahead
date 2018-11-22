package main

import (
	"io/ioutil"
	"time"

	yaml "gopkg.in/yaml.v2"
)

// configSettings contains the key value pairs from the config file
type configSettings struct {
	Timeout                    time.Duration `yaml:"timeout"`
	IncludeDir                 string        `yaml:"include_dir"`
	ListenAddress              string        `yaml:"listen_address"`
	ListenPort                 int           `yaml:"listen_port"`
	PrivateKey                 string        `yaml:"ssl_private_key"`
	CertificateFile            string        `yaml:"ssl_certificate_file"`
	RequireAndVerifyClientCert bool          `yaml:"ssl_require_and_verify_client_cert"`
	ClientCertCaFile           string        `yaml:"ssl_client_cert_ca_file"`
	SaveStateDir               string        `yaml:"save_state_dir"`
	LogBaseDir                 string        `yaml:"log_base_dir"`
}

// readConfigfile creates the configSettings struct from the config file
func readConfigfile(configFile string) configSettings {
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		Fatalf("readConfigfile(): There was an error parsing the config file " + configFile + ": " + err.Error())
	}

	var config configSettings
	err = yaml.Unmarshal([]byte(data), &config)
	if err != nil {
		Fatalf("In config file " + configFile + ": YAML unmarshal error: " + err.Error())
	}

	//fmt.Print("config: ")
	//fmt.Printf("%+v\n", config)

	// set default timeout to 5 seconds if no timeout setting found
	if config.Timeout == 0 {
		config.Timeout = 5
	}

	if !fileExists(config.PrivateKey) {
		Fatalf("Failed to find configured ssl_private_key " + config.PrivateKey)
	}

	if !fileExists(config.CertificateFile) {
		Fatalf("Failed to find configured ssl_certificate_file " + config.CertificateFile)
	}

	if !fileExists(config.ClientCertCaFile) {
		Fatalf("Failed to find configured ssl_client_cert_ca_file " + config.ClientCertCaFile)
	}

	// set default listen address to 0.0.0.0
	if len(config.ListenAddress) < 1 {
		config.ListenAddress = "0.0.0.0"
	}

	// set default save state directory to "/tmp/goahead/"
	if len(config.SaveStateDir) < 1 {
		config.SaveStateDir = "/tmp/goahead/"
	}
	config.SaveStateDir = checkDirAndCreate(config.SaveStateDir, "config setting from config file "+configFile)

	// set default listen port to 8443
	if config.ListenPort == 0 {
		config.ListenPort = 8443
	}

	return config
}
