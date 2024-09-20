package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
)

// serve starts the HTTP server with the configured SSL key and certificate
func serve() {

	router := mux.NewRouter()
	addV1Routes(router.PathPrefix("/v1").Subrouter())
	addRoutes(router)

	// TLS stuff
	tlsConfig := &tls.Config{}
	//Use only TLS v1.2
	tlsConfig.MinVersion = tls.VersionTLS12

	if config.RequireAndVerifyClientCert {

		//Expect and verify client certificate against a CA cert
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert

		// Load CA cert
		caCert, err := os.ReadFile(config.ClientCertCaFile)
		if err != nil {
			mainLogger.Fatal("Error while trying to read ssl_client_cert_ca_file " + config.ClientCertCaFile + " " + err.Error())
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.ClientCAs = caCertPool
		mainLogger.Info("Expecting and verifing client certificate against " + config.ClientCertCaFile)

	}
	server := &http.Server{
		Handler:      router,
		Addr:         config.ListenAddress + ":" + strconv.Itoa(config.ListenPort),
		TLSConfig:    tlsConfig,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	mainLogger.Info("Listening on https://" + config.ListenAddress + ":" + strconv.Itoa(config.ListenPort) + "/")
	err := server.ListenAndServeTLS(config.CertificateFile, config.PrivateKey)
	if err != nil {
		mainLogger.Fatal("Error while trying to serve HTTPS with ssl_certificate_file " + config.CertificateFile + " and ssl_private_key " + config.PrivateKey + " " + err.Error())
	}
}
