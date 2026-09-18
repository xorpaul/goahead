package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
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
	fmt.Println("Trying to read config file: " + configFile)
	data, err := os.ReadFile(configFile)
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

	if !fileExists(config.PrivateKey) || !fileExists(config.CertificateFile) {
		fmt.Println("SSL key or certificate not found, generating self-signed certificate")
		if err := generateSelfSignedCert(config.CertificateFile, config.PrivateKey); err != nil {
			Fatalf("Failed to generate self-signed certificate: " + err.Error())
		}
	}

	if config.RequireAndVerifyClientCert && !fileExists(config.ClientCertCaFile) {
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

// generateSelfSignedCert creates a self-signed ECDSA certificate and key at the given paths.
func generateSelfSignedCert(certFile, keyFile string) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0755); err != nil {
		return err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "goahead-selfsigned"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	keyOut, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}
