package main

import (
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// uiAuthenticator protects the /ui/ routes. The restart API routes are never wrapped by it.
// A request has to come from an allowed network (ui_allowed_cidrs, if set) and, if
// ui_htpasswd_file and/or ui_client_cert_ca_file are set, pass at least one of the two methods.
type uiAuthenticator struct {
	cidrs []*net.IPNet

	htpasswdFile  string
	htpasswdMu    sync.Mutex
	htpasswdMtime time.Time
	users         map[string][]byte

	clientCAs   *x509.CertPool
	clientCAPEM []byte
	clientCNs   map[string]bool
}

// uiAuth is nil while the web interface is not protected
var uiAuth *uiAuthenticator

// dummyBcryptHash is compared against for unknown users, so unknown and wrong users take the same time
var dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("goahead-dummy"), bcrypt.DefaultCost)

// uiAuthFailDelay slows down password guessing
var uiAuthFailDelay = 500 * time.Millisecond

// newUIAuthenticator builds the authenticator from the config, it returns nil if nothing is configured
func newUIAuthenticator(c configSettings) (*uiAuthenticator, error) {
	if len(c.UIAllowedCIDRs) == 0 && c.UIHtpasswdFile == "" && c.UIClientCertCaFile == "" {
		return nil, nil
	}
	a := &uiAuthenticator{htpasswdFile: c.UIHtpasswdFile, clientCNs: map[string]bool{}}
	for _, s := range c.UIAllowedCIDRs {
		s = strings.TrimSpace(s)
		if !strings.Contains(s, "/") {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil, errors.New("ui_allowed_cidrs: " + s + " is neither an IP address nor a CIDR")
			}
			if ip.To4() != nil {
				s += "/32"
			} else {
				s += "/128"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, errors.New("ui_allowed_cidrs: " + err.Error())
		}
		a.cidrs = append(a.cidrs, n)
	}
	if a.htpasswdFile != "" {
		if err := a.loadHtpasswd(); err != nil {
			return nil, err
		}
	}
	if c.UIClientCertCaFile != "" {
		pem, err := os.ReadFile(c.UIClientCertCaFile)
		if err != nil {
			return nil, errors.New("ui_client_cert_ca_file: " + err.Error())
		}
		a.clientCAPEM = pem
		a.clientCAs = x509.NewCertPool()
		if !a.clientCAs.AppendCertsFromPEM(pem) {
			return nil, errors.New("ui_client_cert_ca_file " + c.UIClientCertCaFile + " contains no PEM certificate")
		}
		// a CA that issues certificates to every host must not let every host in
		for _, cn := range c.UIAllowedClientCNs {
			if cn = strings.TrimSpace(cn); cn != "" {
				a.clientCNs[cn] = true
			}
		}
		if len(a.clientCNs) == 0 {
			return nil, errors.New("ui_client_cert_ca_file needs at least one entry in ui_allowed_client_cns")
		}
	}
	return a, nil
}

// loadHtpasswd reads user:bcrypt-hash lines, comments and empty lines are skipped
func (a *uiAuthenticator) loadHtpasswd() error {
	st, err := os.Stat(a.htpasswdFile)
	if err != nil {
		return errors.New("ui_htpasswd_file: " + err.Error())
	}
	data, err := os.ReadFile(a.htpasswdFile)
	if err != nil {
		return errors.New("ui_htpasswd_file: " + err.Error())
	}
	users := map[string][]byte{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		user, hash, ok := strings.Cut(line, ":")
		if !ok || user == "" {
			return errors.New("ui_htpasswd_file " + a.htpasswdFile + ": line without user:hash")
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			return errors.New("ui_htpasswd_file " + a.htpasswdFile + ": only bcrypt hashes are supported (htpasswd -B), user " + user)
		}
		users[user] = []byte(hash)
	}
	if len(users) == 0 {
		return errors.New("ui_htpasswd_file " + a.htpasswdFile + " contains no user")
	}
	a.htpasswdMu.Lock()
	a.users, a.htpasswdMtime = users, st.ModTime()
	a.htpasswdMu.Unlock()
	return nil
}

// userHash returns the hash of a user and reloads the file if it changed, a broken new file keeps the old users
func (a *uiAuthenticator) userHash(user string) []byte {
	if st, err := os.Stat(a.htpasswdFile); err == nil {
		a.htpasswdMu.Lock()
		changed := !st.ModTime().Equal(a.htpasswdMtime)
		a.htpasswdMu.Unlock()
		if changed {
			if err := a.loadHtpasswd(); err != nil {
				mainLogger.Warn("keeping the old web interface users: " + err.Error())
			}
		}
	}
	a.htpasswdMu.Lock()
	defer a.htpasswdMu.Unlock()
	return a.users[user]
}

func (a *uiAuthenticator) ipAllowed(remoteAddr string) bool {
	if len(a.cidrs) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range a.cidrs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// certIdentity returns the CN of the client certificate if it chains to ui_client_cert_ca_file and is allowed
func (a *uiAuthenticator) certIdentity(r *http.Request) (string, bool) {
	if a.clientCAs == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", false
	}
	leaf := r.TLS.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, c := range r.TLS.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	// verify against the web interface CA itself, the listener may trust more CAs for the API
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: a.clientCAs, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return "", false
	}
	if a.clientCNs[leaf.Subject.CommonName] {
		return leaf.Subject.CommonName, true
	}
	for _, san := range leaf.DNSNames {
		if a.clientCNs[san] {
			return san, true
		}
	}
	return "", false
}

// authenticate returns the identity for the log line
func (a *uiAuthenticator) authenticate(r *http.Request) (string, bool) {
	if a.clientCAs == nil && a.htpasswdFile == "" {
		return "network:" + r.RemoteAddr, true // only the network allowlist is configured
	}
	if id, ok := a.certIdentity(r); ok {
		return "cert:" + id, true
	}
	if a.htpasswdFile != "" {
		if user, pass, ok := r.BasicAuth(); ok {
			hash := a.userHash(user)
			known := hash != nil
			if !known {
				hash = dummyBcryptHash
			}
			if bcrypt.CompareHashAndPassword(hash, []byte(pass)) == nil && known {
				return "user:" + user, true
			}
		}
	}
	return "", false
}

// uiProtect wraps a handler of the web interface
func uiProtect(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := uiAuth
		if a == nil {
			h(w, r)
			return
		}
		if !a.ipAllowed(r.RemoteAddr) {
			mainLogger.Warn("web interface: denied " + r.RemoteAddr + " " + r.URL.Path + ": source address not in ui_allowed_cidrs")
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		id, ok := a.authenticate(r)
		if !ok {
			time.Sleep(uiAuthFailDelay)
			mainLogger.Warn("web interface: authentication failed for " + r.RemoteAddr + " " + r.URL.Path)
			if a.htpasswdFile != "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="goahead", charset="UTF-8"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
			} else {
				http.Error(w, "forbidden", http.StatusForbidden)
			}
			return
		}
		mainLogger.Info("web interface: " + id + " from " + r.RemoteAddr + " " + r.Method + " " + r.URL.Path)
		h(w, r)
	}
}
