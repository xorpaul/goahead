package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/crypto/bcrypt"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, cn string) testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return testCA{c, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca testCA) issue(t *testing.T, cn string) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func authRouter(t *testing.T, c configSettings) *mux.Router {
	t.Helper()
	r := setupUITest(t)
	a, err := newUIAuthenticator(c)
	if err != nil {
		t.Fatal(err)
	}
	uiAuth = a
	t.Cleanup(func() { uiAuth = nil })
	uiAuthFailDelay = 0
	return r
}

func do(r http.Handler, path, remote string, mod func(*http.Request)) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remote
	if mod != nil {
		mod(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func writeHtpasswd(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "htpasswd")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func hash(t *testing.T, pw string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func TestUIBasicAuth(t *testing.T) {
	f := writeHtpasswd(t, "# team\nalice:"+hash(t, "s3cret")+"\n")
	r := authRouter(t, configSettings{UIHtpasswdFile: f})
	ip := "10.0.0.1:1234"
	if c := do(r, "/ui/", ip, nil); c != 401 {
		t.Fatalf("no credentials: %d", c)
	}
	for _, cred := range [][2]string{{"alice", "wrong"}, {"bob", "s3cret"}, {"", ""}} {
		if c := do(r, "/ui/panic", ip, func(q *http.Request) { q.SetBasicAuth(cred[0], cred[1]) }); c != 401 {
			t.Fatalf("%v: %d", cred, c)
		}
	}
	for _, p := range []string{"/ui/", "/ui/panic", "/ui/activity", "/ui/status.json", "/ui/cluster/es"} {
		if c := do(r, p, ip, func(q *http.Request) { q.SetBasicAuth("alice", "s3cret") }); c != 200 {
			t.Fatalf("%s with credentials: %d", p, c)
		}
		if c := do(r, p, ip, nil); c != 401 {
			t.Fatalf("%s without credentials: %d", p, c)
		}
	}
	// a changed file is picked up without a restart
	os.WriteFile(f, []byte("carol:"+hash(t, "pw2")+"\n"), 0600)
	future := time.Now().Add(time.Minute)
	os.Chtimes(f, future, future)
	if c := do(r, "/ui/", ip, func(q *http.Request) { q.SetBasicAuth("carol", "pw2") }); c != 200 {
		t.Fatalf("reloaded user: %d", c)
	}
	if c := do(r, "/ui/", ip, func(q *http.Request) { q.SetBasicAuth("alice", "s3cret") }); c != 401 {
		t.Fatalf("removed user: %d", c)
	}
	// a broken file keeps the old users
	os.WriteFile(f, []byte("garbage\n"), 0600)
	future = future.Add(time.Minute)
	os.Chtimes(f, future, future)
	if c := do(r, "/ui/", ip, func(q *http.Request) { q.SetBasicAuth("carol", "pw2") }); c != 200 {
		t.Fatalf("broken file: %d", c)
	}
}

func TestUIHtpasswdRejectsNonBcrypt(t *testing.T) {
	f := writeHtpasswd(t, "alice:{SHA}W6ph5Mm5Pz8GgiULbPgzG37mj9g=\n")
	if _, err := newUIAuthenticator(configSettings{UIHtpasswdFile: f}); err == nil {
		t.Fatal("expected an error for a non bcrypt hash")
	}
	if _, err := newUIAuthenticator(configSettings{UIHtpasswdFile: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestUIAllowedCIDRs(t *testing.T) {
	r := authRouter(t, configSettings{UIAllowedCIDRs: []string{"10.1.0.0/16", "192.0.2.7", "2001:db8::/32"}})
	for remote, want := range map[string]int{"10.1.2.3:5": 200, "192.0.2.7:9": 200, "[2001:db8::1]:9": 200, "10.2.0.1:5": 403, "192.0.2.8:9": 403, "bogus": 403} {
		if c := do(r, "/ui/", remote, nil); c != want {
			t.Errorf("%s: got %d want %d", remote, c, want)
		}
	}
	if _, err := newUIAuthenticator(configSettings{UIAllowedCIDRs: []string{"10.0.0.0/33"}}); err == nil {
		t.Error("expected an error for a bad CIDR")
	}
}

func TestUIClientCert(t *testing.T) {
	ca, other := newTestCA(t, "internal ca"), newTestCA(t, "other ca")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, ca.pem, 0644)
	cfg := configSettings{UIClientCertCaFile: caFile, UIAllowedClientCNs: []string{"alice"}}
	r := authRouter(t, cfg)
	ip := "10.0.0.1:1"
	withCert := func(c *x509.Certificate) func(*http.Request) {
		return func(q *http.Request) { q.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}} }
	}
	if c := do(r, "/ui/", ip, withCert(ca.issue(t, "alice"))); c != 200 {
		t.Errorf("allowed cert: %d", c)
	}
	if c := do(r, "/ui/", ip, withCert(ca.issue(t, "some-server"))); c != 403 {
		t.Errorf("cert of the right CA but not allowed: %d", c)
	}
	if c := do(r, "/ui/", ip, withCert(other.issue(t, "alice"))); c != 403 {
		t.Errorf("allowed name from another CA: %d", c)
	}
	if c := do(r, "/ui/", ip, nil); c != 403 {
		t.Errorf("no cert: %d", c)
	}
	if _, err := newUIAuthenticator(configSettings{UIClientCertCaFile: caFile}); err == nil {
		t.Error("a CA without ui_allowed_client_cns must be refused")
	}
}

func TestUIAuthMethodsAreAlternativesAndCIDRsAlwaysApply(t *testing.T) {
	ca := newTestCA(t, "internal ca")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, ca.pem, 0644)
	f := writeHtpasswd(t, "alice:"+hash(t, "pw")+"\n")
	r := authRouter(t, configSettings{UIClientCertCaFile: caFile, UIAllowedClientCNs: []string{"bob"}, UIHtpasswdFile: f, UIAllowedCIDRs: []string{"10.0.0.0/8"}})
	cert := ca.issue(t, "bob")
	if c := do(r, "/ui/", "10.0.0.1:1", func(q *http.Request) { q.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}} }); c != 200 {
		t.Errorf("cert only: %d", c)
	}
	if c := do(r, "/ui/", "10.0.0.1:1", func(q *http.Request) { q.SetBasicAuth("alice", "pw") }); c != 200 {
		t.Errorf("password only: %d", c)
	}
	if c := do(r, "/ui/", "198.51.100.1:1", func(q *http.Request) { q.SetBasicAuth("alice", "pw") }); c != 403 {
		t.Errorf("valid password from a denied network: %d", c)
	}
}

func TestAPIRoutesNotProtectedByUIAuth(t *testing.T) {
	r := authRouter(t, configSettings{UIHtpasswdFile: writeHtpasswd(t, "alice:"+hash(t, "pw")+"\n")})
	addV1Routes(r.PathPrefix("/v1").Subrouter())
	if c := do(r, "/v1/inquire/restart/node/", "10.0.0.1:1", nil); c == 401 || c == 403 {
		t.Errorf("API route got %d from the web interface auth", c)
	}
}
