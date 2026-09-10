package toolkit

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"git-tools/finding"
)

var networkLookup = net.DefaultResolver.LookupIPAddr
var networkDial = (&net.Dialer{}).DialContext

func networkFailure(err error) string {
	var dns *net.DNSError
	var timeout net.Error
	var host x509.HostnameError
	var authority x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	switch {
	case errors.As(err, &host):
		return "certificate hostname mismatch"
	case errors.As(err, &authority):
		return "certificate issuer is not trusted"
	case errors.As(err, &certificate):
		return "certificate validity check failed"
	case errors.As(err, &dns) && dns.IsNotFound:
		return "DNS name not found"
	case errors.As(err, &timeout) && timeout.Timeout():
		return "operation timed out"
	default:
		return "connection, protocol or verification failure; raw diagnostics withheld"
	}
}
func parseNetworkTarget(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || !oneOf(u.Scheme, "http", "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !operationLabel(raw) || markdownSafe(raw) != raw {
		return nil, fmt.Errorf("use an http/https URL without credentials, query, fragment or control characters")
	}
	if strings.Contains(u.Hostname(), "%") {
		return nil, fmt.Errorf("scoped IPv6 addresses are unsupported")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid TCP port")
		}
	}
	return u, nil
}
func probeNetwork(ctx context.Context, u *url.URL, expected int, minValid time.Duration, roots *x509.CertPool) finding.Report {
	r := finding.Report{CompletedChecks: []string{"Network observation: " + time.Now().UTC().Format(time.RFC3339Nano), "One direct endpoint probe; redirects, proxies, credentials and response bodies are not used", "DNS answers and a successful endpoint response do not verify every backend or application dependency"}}
	resource := safeReportText(u.Scheme + "://" + u.Host + u.EscapedPath())
	failed := func(stage string, err error, skipped string) {
		addIAC(&r, "NET-"+stage, finding.SeverityHigh, stage+" check failed", resource, "probe", "unknown", networkFailure(err))
		if skipped != "" {
			r.IncompleteChecks = append(r.IncompleteChecks, skipped+" not attempted because "+stage+" failed")
		}
	}
	ips, err := networkLookup(ctx, u.Hostname())
	if err != nil || len(ips) == 0 {
		if err == nil {
			err = fmt.Errorf("no DNS addresses")
		}
		failed("DNS", err, "TCP/TLS/HTTP")
		return r
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("DNS: %d addresses returned for %s", len(ips), safeReportText(u.Hostname())))
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	var conn net.Conn
	attempts := 0
	selected := ""
	for _, ip := range ips {
		if attempts == 8 {
			break
		}
		attempts++
		address := net.JoinHostPort(ip.IP.String(), port)
		conn, err = networkDial(ctx, "tcp", address)
		if err == nil {
			selected = address
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	if conn == nil {
		if err == nil {
			err = fmt.Errorf("no usable address")
		}
		failed("TCP", err, "TLS/HTTP")
		if len(ips) > attempts {
			r.IncompleteChecks = append(r.IncompleteChecks, "Additional resolved addresses were not attempted within the probe limits")
		}
		return r
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("TCP: connected to %s after %d attempts; other addresses are not certified", selected, attempts))
	if attempts > 1 {
		addIAC(&r, "NET-ADDRESS", finding.SeverityWarning, "Earlier address attempts failed", resource, "probe", "unknown", fmt.Sprintf("%d attempts failed before this connection succeeded", attempts-1))
	}
	if u.Scheme == "https" {
		secure := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, RootCAs: roots, NextProtos: []string{"http/1.1"}})
		if err = secure.HandshakeContext(ctx); err != nil {
			failed("TLS", err, "HTTP")
			return r
		}
		conn = secure
		state := secure.ConnectionState()
		r.CompletedChecks = append(r.CompletedChecks, "TLS: trusted certificate chain and hostname verified; protocol "+tls.VersionName(state.Version))
		if len(state.PeerCertificates) > 0 {
			expiry := state.PeerCertificates[0].NotAfter
			r.CompletedChecks = append(r.CompletedChecks, "Leaf certificate expires: "+expiry.UTC().Format(time.RFC3339))
			if time.Until(expiry) < minValid {
				addIAC(&r, "NET-EXPIRY", finding.SeverityWarning, "Certificate expires within requested window", resource, "probe", "unknown", "Expiry: "+expiry.UTC().Format(time.RFC3339))
			}
		}
	} else {
		r.CompletedChecks = append(r.CompletedChecks, "TLS: not applicable to an HTTP URL")
	}
	// Reuse the verified socket, preserving the DNS/TCP/TLS evidence chain.
	used := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		if used {
			return nil, fmt.Errorf("a second connection is not allowed")
		}
		used = true
		return conn, nil
	}
	transport := &http.Transport{Proxy: nil, DialContext: dial, DialTLSContext: dial, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10, ForceAttemptHTTP2: false}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		failed("HTTP", err, "")
		return r
	}
	req.Header.Set("User-Agent", "rcdo-network-check")
	response, err := client.Do(req)
	if err != nil {
		failed("HTTP", err, "")
		return r
	}
	defer response.Body.Close()
	r.CompletedChecks = append(r.CompletedChecks, fmt.Sprintf("HTTP HEAD: received status %d; expected %d; response headers and body withheld", response.StatusCode, expected))
	if response.StatusCode != expected {
		addIAC(&r, "NET-STATUS", finding.SeverityHigh, "HTTP status differs from expectation", resource, "probe", "unknown", fmt.Sprintf("Observed %d; expected %d. Redirects are not followed.", response.StatusCode, expected))
	}
	return r
}
func runNetworkCheck(args []string, stdout, stderr io.Writer) error {
	var target string
	var timeout, minValid time.Duration
	var expected int
	_, o, err := parseFlags("network-check", args, stderr, func(fs *flag.FlagSet) *commonOptions {
		var o commonOptions
		addCommonFlags(fs, &o)
		fs.StringVar(&target, "url", "", "explicit endpoint to probe using HEAD")
		fs.DurationVar(&timeout, "timeout", 10*time.Second, "total probe deadline, at most 60s")
		fs.DurationVar(&minValid, "min-valid-for", 24*time.Hour, "warn if certificate expires within this interval")
		fs.IntVar(&expected, "expect-status", 200, "expected HTTP status, 200..599")
		return &o
	})
	if err != nil {
		return err
	}
	if timeout <= 0 || timeout > 60*time.Second || minValid < 0 || expected < 200 || expected > 599 || o.input != "-" || o.policy != "" {
		return fmt.Errorf("invalid probe limits or unsupported input/policy")
	}
	u, err := parseNetworkTarget(target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return emitReportOptions(stdout, o, probeNetwork(ctx, u, expected, minValid, nil))
}
