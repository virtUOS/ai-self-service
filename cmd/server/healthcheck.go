package main

import (
	"fmt"
	"net"
	"net/http"
	"time"
)

// healthcheckURL turns the listen address into the /healthz URL the
// -healthcheck mode requests. A wildcard address listens on loopback too, so
// the check stays inside the container; a specific host is used as is.
func healthcheckURL(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("LISTEN_ADDR %q: %w", listenAddr, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// checkHealth reports whether the server answers url with 200. The timeout
// stays below the Dockerfile's HEALTHCHECK --timeout so a hung server is
// reported by us rather than killed by the runtime.
func checkHealth(url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return nil
}
