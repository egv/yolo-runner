package opencode

import (
	"net"
	"strconv"
	"strings"
)

func resolveServeBaseURL(hostname string, port int) string {
	resolvedHostname := strings.TrimSpace(hostname)
	if resolvedHostname == "" {
		resolvedHostname = defaultServeHostname
	}
	resolvedHostname = strings.TrimPrefix(resolvedHostname, "[")
	resolvedHostname = strings.TrimSuffix(resolvedHostname, "]")
	if ip := net.ParseIP(resolvedHostname); ip != nil && ip.IsUnspecified() {
		resolvedHostname = "localhost"
	}
	return "http://" + net.JoinHostPort(resolvedHostname, strconv.Itoa(port))
}

func resolveServeHealthURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/global/health"
}
