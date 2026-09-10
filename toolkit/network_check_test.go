package toolkit

import (
	"context"
	"crypto/x509"
	"errors"
	"git-tools/finding"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNetworkHTTPNoRedirectOrCredentials(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "HEAD" || r.URL.Path != "/health" || r.Header.Get("Authorization") != "" {
			t.Error("unsafe request")
		}
		w.Header().Set("Location", "http://unrequested.invalid/")
		w.Header().Set("Secret", "never-print")
		w.WriteHeader(302)
	}))
	defer s.Close()
	code, out, e := execute("network-check", []string{"--url", s.URL + "/health", "--timeout", "2s"}, "")
	if code != 20 || calls != 1 || strings.Contains(out, "never-print") || !strings.Contains(out, "302") || !strings.Contains(out, "/health") {
		t.Fatalf("%d %s %s calls=%d", code, out, e, calls)
	}
	for _, raw := range []string{"http://user:pass@example.com", "http://example.com/?token=x", "file:///tmp/x", "http://example.com/#x"} {
		if _, e := parseNetworkTarget(raw); e == nil {
			t.Fatal("invalid target accepted", raw)
		}
	}
}
func TestNetworkTLSVerification(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	u, _ := parseNetworkTarget(s.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := probeNetwork(ctx, u, 204, 0, nil)
	if r.Status() != finding.StatusIncomplete || len(r.Findings) == 0 {
		t.Fatal("untrusted TLS accepted", r)
	}
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	r = probeNetwork(ctx, u, 204, 0, pool)
	if r.Status() != finding.StatusClean {
		t.Fatal(r)
	}
}
func TestNetworkDNSFailureStopsLaterStages(t *testing.T) {
	oldLookup, oldDial := networkLookup, networkDial
	defer func() { networkLookup = oldLookup; networkDial = oldDial }()
	networkLookup = func(context.Context, string) ([]net.IPAddr, error) {
		return nil, &net.DNSError{IsNotFound: true, Err: "secret diagnostic"}
	}
	networkDial = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial after DNS failure")
		return nil, errors.New("unexpected")
	}
	code, out, e := execute("network-check", []string{"--url", "https://missing.example"}, "")
	if code != 30 || strings.Contains(out+e, "secret diagnostic") || !strings.Contains(out, "DNS name not found") {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
func TestNetworkDeadlineBoundsSilentHTTP(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	start := time.Now()
	code, out, e := execute("network-check", []string{"--url", s.URL, "--timeout", "50ms"}, "")
	if code != 20 || time.Since(start) > time.Second {
		t.Fatalf("%d %s %s", code, out, e)
	}
}
