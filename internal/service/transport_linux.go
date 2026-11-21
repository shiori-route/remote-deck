package service

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Validate transport before touching the database or opening any listener.
func transportConfig(c Config) (*tls.Config, error) {
	host, port, err := net.SplitHostPort(c.Address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || ip.To4() == nil {
		return nil, errors.New("address must be an IPv4 literal and port")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return nil, errors.New("invalid port")
	}
	if c.Origin == "" && c.TLSCert == "" && c.TLSKey == "" {
		if host != "127.0.0.1" {
			return nil, errors.New("HTTP requires 127.0.0.1; remote access requires origin, tls-cert and tls-key")
		}
		return nil, nil
	}
	if c.Origin == "" || c.TLSCert == "" || c.TLSKey == "" {
		return nil, errors.New("origin, tls-cert and tls-key must be configured together")
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != c.Origin || strings.ContainsAny(c.Origin, "\\%#") {
		return nil, errors.New("origin must be an exact HTTPS origin without credentials, path, query or fragment")
	}
	// Require canonical browser serialization, including omission of default :443.
	if u.Host != strings.ToLower(u.Host) || strings.HasSuffix(u.Hostname(), ".") || strings.HasSuffix(u.Host, ":") || u.Port() == "443" {
		return nil, errors.New("origin must use lowercase hostname, no trailing dot and no explicit default port")
	}
	if op := u.Port(); op != "" {
		n, e := strconv.Atoi(op)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != op {
			return nil, errors.New("invalid origin port")
		}
	}
	if originIP := net.ParseIP(u.Hostname()); originIP != nil && (originIP.IsUnspecified() || originIP.IsMulticast()) {
		return nil, errors.New("origin must identify a specific host")
	}
	certPEM, err := os.ReadFile(c.TLSCert)
	if err != nil {
		return nil, fmt.Errorf("read TLS certificate: %w", err)
	}
	keyPEM, err := readTLSKey(c.TLSKey)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load TLS key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err = leaf.VerifyHostname(u.Hostname()); err != nil {
		return nil, fmt.Errorf("certificate does not cover origin: %w", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("TLS certificate is not currently valid")
	}
	if len(leaf.ExtKeyUsage) > 0 {
		usable := false
		for _, usage := range leaf.ExtKeyUsage {
			usable = usable || usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny
		}
		if !usable {
			return nil, errors.New("TLS certificate does not permit server authentication")
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}, nil
}

func readTLSKey(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open TLS key: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Size() > 1<<20 {
		return nil, errors.New("TLS key must be a private regular file, owned by service user, mode 0600, with one link and at most 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if len(data) > 1<<20 {
		return nil, errors.New("TLS key exceeds 1 MiB")
	}
	return data, err
}
