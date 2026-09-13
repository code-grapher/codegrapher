package cli

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type apiListenerSettings struct {
	listenAddress   string
	certificate     string
	privateKey      string
	publicAuthority string
	externalTLS     bool
}

type apiListener struct {
	net.Listener
	scheme    string
	authority string
}

func openAPIListener(settings apiListenerSettings) (*apiListener, error) {
	bindHost, bindPort, err := splitListenAddress(settings.listenAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid browser API listen address: %w", err)
	}
	loopback := isLoopbackHost(bindHost)
	hasCert := strings.TrimSpace(settings.certificate) != ""
	hasKey := strings.TrimSpace(settings.privateKey) != ""
	if hasCert != hasKey {
		return nil, fmt.Errorf("--api-tls-cert and --api-tls-key must be supplied together")
	}
	if settings.externalTLS && (hasCert || hasKey) {
		return nil, fmt.Errorf("--api-external-tls cannot be combined with direct TLS certificate flags")
	}
	if settings.externalTLS && !loopback {
		return nil, fmt.Errorf("external TLS termination requires a loopback --api-listen address")
	}
	if !loopback && !hasCert {
		return nil, fmt.Errorf("non-loopback browser API listeners require direct TLS")
	}
	if settings.publicAuthority != "" && !hasCert && !settings.externalTLS {
		return nil, fmt.Errorf("--api-public-authority requires direct TLS or --api-external-tls")
	}

	publicHost, publicPort, publicHasPort, err := parsePublicAuthority(settings.publicAuthority)
	if err != nil {
		return nil, err
	}
	if hasCert || settings.externalTLS {
		if publicHost == "" {
			return nil, fmt.Errorf("--api-public-authority is required for HTTPS serving")
		}
	}
	publicAuthority := canonicalPublicAuthority(publicHost, publicPort, publicHasPort)
	if hasCert && bindPort != 0 {
		effectivePublicPort := 443
		if publicHasPort {
			effectivePublicPort = publicPort
		}
		if effectivePublicPort != bindPort {
			return nil, fmt.Errorf("direct TLS public authority port %d does not match listen port %d", effectivePublicPort, bindPort)
		}
	}
	if hasCert && bindPort == 0 && publicHasPort {
		return nil, fmt.Errorf("direct TLS with an ephemeral listen port cannot use an explicit public authority port")
	}

	var tlsConfig *tls.Config
	if hasCert {
		pair, err := tls.LoadX509KeyPair(settings.certificate, settings.privateKey)
		if err != nil {
			return nil, fmt.Errorf("load browser API TLS key pair: %w", err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parse browser API TLS certificate: %w", err)
		}
		now := time.Now()
		if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
			return nil, fmt.Errorf("browser API TLS certificate is not currently valid")
		}
		if err := leaf.VerifyHostname(publicHost); err != nil {
			return nil, fmt.Errorf("browser API TLS certificate does not cover %q: %w", publicHost, err)
		}
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	}

	listener, err := net.Listen("tcp", settings.listenAddress)
	if err != nil {
		return nil, fmt.Errorf("bind browser API: %w", err)
	}
	scheme := "http"
	authority := listener.Addr().String()
	if hasCert {
		scheme = "https"
		authority = publicAuthority
		if bindPort == 0 && !publicHasPort {
			_, actualPort, splitErr := splitListenAddress(listener.Addr().String())
			if splitErr != nil {
				_ = listener.Close()
				return nil, splitErr
			}
			authority = net.JoinHostPort(publicHost, strconv.Itoa(actualPort))
		}
		listener = tls.NewListener(listener, tlsConfig)
	} else if settings.externalTLS {
		scheme = "https"
		authority = publicAuthority
	}
	return &apiListener{Listener: listener, scheme: scheme, authority: authority}, nil
}

func splitListenAddress(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portText)
	}
	return strings.Trim(host, "[]"), port, nil
}

func isLoopbackHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}

func parsePublicAuthority(authority string) (host string, port int, hasPort bool, err error) {
	if authority == "" {
		return "", 0, false, nil
	}
	if authority != strings.TrimSpace(authority) {
		return "", 0, false, fmt.Errorf("invalid --api-public-authority %q", authority)
	}
	if strings.ContainsAny(authority, "/?#@ \\") {
		return "", 0, false, fmt.Errorf("invalid --api-public-authority %q", authority)
	}
	if strings.Count(authority, ":") > 1 && !strings.HasPrefix(authority, "[") {
		return "", 0, false, fmt.Errorf("IPv6 public authorities must be bracketed")
	}
	parsed, parseErr := url.Parse("//" + authority)
	if parseErr != nil || parsed.Host != authority || parsed.Hostname() == "" || parsed.User != nil {
		return "", 0, false, fmt.Errorf("invalid --api-public-authority %q", authority)
	}
	host = parsed.Hostname()
	if strings.HasPrefix(parsed.Host, "[") {
		ip := net.ParseIP(host)
		if ip == nil {
			return "", 0, false, fmt.Errorf("invalid --api-public-authority %q", authority)
		}
		host = ip.String()
	} else if isIPv4Candidate(host) {
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil || ip.String() != host {
			return "", 0, false, fmt.Errorf("invalid --api-public-authority %q", authority)
		}
	} else {
		for _, label := range strings.Split(host, ".") {
			if !validDNSLabel(label) {
				return "", 0, false, fmt.Errorf("invalid --api-public-authority %q", authority)
			}
		}
		host = strings.ToLower(host)
	}
	portText := parsed.Port()
	if portText == "" {
		return host, 0, false, nil
	}
	port, err = strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false, fmt.Errorf("invalid public authority port %q", portText)
	}
	return host, port, true, nil
}

func canonicalPublicAuthority(host string, port int, hasPort bool) string {
	if host == "" {
		return ""
	}
	if hasPort && port != 443 {
		return net.JoinHostPort(host, strconv.Itoa(port))
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func isIPv4Candidate(host string) bool {
	if host == "" {
		return false
	}
	for _, char := range host {
		if (char < '0' || char > '9') && char != '.' {
			return false
		}
	}
	return true
}

func validDNSLabel(label string) bool {
	if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, char := range label {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}
