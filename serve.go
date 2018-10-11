package main

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

func httpHandler(w http.ResponseWriter, r *http.Request) {
	ip := strings.Split(r.RemoteAddr, ":")[0]
	method := r.Method
	rid := randSeq()
	Debugf(rid + "Incoming " + method + " request from IP: " + ip)

	switch method {
	case "GET", "POST":
		Debugf(rid + "Request path: " + r.URL.Path)
	}
}

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
		caCert, err := ioutil.ReadFile(config.ClientCertCaFile)
		if err != nil {
			Fatalf("Error while trying to read ssl_client_cert_ca_file " + config.ClientCertCaFile + " " + err.Error())
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.ClientCAs = caCertPool
		Infof("Expecting and verifing client certificate against " + config.ClientCertCaFile)

	}
	server := &http.Server{
		Handler:      router,
		Addr:         config.ListenAddress + ":" + strconv.Itoa(config.ListenPort),
		TLSConfig:    tlsConfig,
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	Infof("Listening on https://" + config.ListenAddress + ":" + strconv.Itoa(config.ListenPort) + "/")
	err := server.ListenAndServeTLS(config.CertificateFile, config.PrivateKey)
	if err != nil {
		Fatalf("Error while trying to serve HTTPS with ssl_certificate_file " + config.CertificateFile + " and ssl_private_key " + config.PrivateKey + " " + err.Error())
	}
}
