package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"remotedeck/internal/auth"
	"remotedeck/internal/job"
)

func tlsFixture(t *testing.T, from, until time.Time) (Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "RemoteDeck test"}, DNSNames: []string{"deck.example.test"}, NotBefore: from, NotAfter: until, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := privateDir(t)
	c := Config{Address: "127.0.0.1:0", DataDirectory: filepath.Join(dir, "data"), Origin: "https://deck.example.test:8443", TLSCert: filepath.Join(dir, "cert.pem"), TLSKey: filepath.Join(dir, "key.pem")}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for path, data := range map[string][]byte{c.TLSCert: certPEM, c.TLSKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	return c, roots
}

func TestTransportConfiguration(t *testing.T) {
	now := time.Now()
	c, _ := tlsFixture(t, now.Add(-time.Hour), now.Add(time.Hour))
	for _, address := range []string{"127.0.0.1:0", "192.168.1.10:8443", "0.0.0.0:8443"} {
		candidate := c
		candidate.Address = address
		cfg, err := transportConfig(candidate)
		if err != nil || cfg.MinVersion != tls.VersionTLS12 {
			t.Fatalf("TLS config %s: %v", address, err)
		}
	}
	for _, origin := range []string{"http://deck.example.test", "https://deck.example.test/", "https://user@deck.example.test", "https://deck.example.test?", "https://deck.example.test#", "https://deck.example.test:443", "https://deck.example.test:08443", "https://DECK.example.test", "https://deck.example.test.", "https://other.example.test", "https://0.0.0.0", "https://deck.example.test:99999", "https://deck.example.test:"} {
		candidate := c
		candidate.Origin = origin
		if _, err := transportConfig(candidate); err == nil {
			t.Errorf("accepted origin %q", origin)
		}
	}
	for _, missing := range []string{"origin", "cert", "key"} {
		candidate := c
		switch missing {
		case "origin":
			candidate.Origin = ""
		case "cert":
			candidate.TLSCert = ""
		case "key":
			candidate.TLSKey = ""
		}
		if _, err := Open(candidate, testLogger()); err == nil {
			t.Fatal("accepted incomplete TLS config")
		}
		if _, err := os.Stat(c.DataDirectory); !os.IsNotExist(err) {
			t.Fatal("bad TLS config touched data directory")
		}
	}
	if err := os.Chmod(c.TLSKey, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := transportConfig(c); err == nil {
		t.Fatal("accepted shared TLS key")
	}
	if err := os.Chmod(c.TLSKey, 0600); err != nil {
		t.Fatal(err)
	}
	link := c.TLSKey + ".link"
	if err := os.Symlink(c.TLSKey, link); err != nil {
		t.Fatal(err)
	}
	candidate := c
	candidate.TLSKey = link
	if _, err := transportConfig(candidate); err == nil {
		t.Fatal("accepted key symlink")
	}
	for _, times := range [][2]time.Time{{now.Add(-2 * time.Hour), now.Add(-time.Hour)}, {now.Add(time.Hour), now.Add(2 * time.Hour)}} {
		candidate, _ := tlsFixture(t, times[0], times[1])
		if _, err := transportConfig(candidate); err == nil {
			t.Fatal("accepted invalid certificate dates")
		}
	}
}

func TestActualHTTPSService(t *testing.T) {
	c, roots := tlsFixture(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	s, err := Open(c, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, s.Address())
	}
	transport := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: transport, Jar: jar, Timeout: 3 * time.Second}
	data, err := os.ReadFile(filepath.Join(c.DataDirectory, auth.TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(data))
	request := func(method, path, origin, csrf, bearer string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(method, c.Origin+path, strings.NewReader(`{"task_type":"system-info","parameters":{}}`))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	check := func(res *http.Response, status int) {
		t.Helper()
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("status %d want %d", res.StatusCode, status)
		}
	}
	check(request("GET", "/", "", "", ""), 200)
	check(request("GET", "/api/v1/jobs", "", "", ""), 401)
	res := request("POST", "/api/v1/session", c.Origin, "", token)
	if res.StatusCode != 201 {
		t.Fatalf("login %d", res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-remotedeck_session" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].Domain != "" {
		t.Fatal("unsafe HTTPS cookie")
	}
	var session struct {
		Data auth.Session `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	check(request("GET", "/api/v1/session", "", "", ""), 200)
	check(request("POST", "/api/v1/jobs", c.Origin, "", ""), 403)
	check(request("POST", "/api/v1/jobs", "https://other.example.test", session.Data.CSRFToken, ""), 403)
	check(request("GET", "/api/v1/jobs", "", "", "invalid"), 401)
	res = request("POST", "/api/v1/jobs", c.Origin, session.Data.CSRFToken, "")
	if res.StatusCode != 201 {
		t.Fatal("HTTPS submission failed")
	}
	var entry struct {
		Data job.Job `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	id := entry.Data.ID
	deadline := time.Now().Add(5 * time.Second)
	for !entry.Data.State.Terminal() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		res = request("GET", "/api/v1/jobs/"+id, "", "", "")
		if err := json.NewDecoder(res.Body).Decode(&entry); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
	if entry.Data.State != job.Succeeded {
		t.Fatal("HTTPS job did not succeed")
	}
	res = request("GET", "/api/v1/jobs/"+id+"/logs", "", "", "")
	logs, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || !strings.Contains(string(logs), "Linux") {
		t.Fatal("HTTPS logs missing")
	}
	// A valid bearer and forwarded headers cannot bypass the configured Host.
	r, _ := http.NewRequest("GET", c.Origin+"/api/v1/jobs", nil)
	r.Host = "attacker.test"
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Forwarded-Host", "deck.example.test:8443")
	r.Header.Set("X-Forwarded-Proto", "https")
	res, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	check(res, 403)
	check(request("DELETE", "/api/v1/session", c.Origin, session.Data.CSRFToken, ""), 204)
	r, _ = http.NewRequest("GET", c.Origin+"/api/v1/jobs", nil)
	r.AddCookie(cookies[0])
	res, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	check(res, 401)
	check(request("GET", "/api/v1/jobs", "", "", token), 200)
	// Plain HTTP must never reach the application on the TLS port.
	plain := &http.Client{Timeout: 3 * time.Second}
	res, err = plain.Get("http://" + s.Address() + "/")
	if err == nil {
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("plaintext reached console")
		}
	}
	for name, cfg := range map[string]*tls.Config{"untrusted": {MinVersion: tls.VersionTLS12}, "obsolete TLS": {RootCAs: roots, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}} {
		tr := &http.Transport{DialContext: dial, TLSClientConfig: cfg}
		cl := &http.Client{Transport: tr, Timeout: 3 * time.Second}
		response, e := cl.Get(c.Origin + "/")
		tr.CloseIdleConnections()
		if e == nil {
			response.Body.Close()
			t.Errorf("accepted %s", name)
		}
	}
}
