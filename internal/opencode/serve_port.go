package opencode

import (
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

const (
	defaultServePortBase = 38000
	defaultServePortSpan = 2000
)

func AllocateServePort(hostname string, request contracts.TaskSessionStartRequest) (int, error) {
	resolvedHostname := strings.TrimSpace(hostname)
	if resolvedHostname == "" {
		resolvedHostname = defaultServeHostname
	}

	start := deterministicServePortStart(request)
	for offset := 0; offset < defaultServePortSpan; offset++ {
		port := defaultServePortBase + ((start - defaultServePortBase + offset) % defaultServePortSpan)
		if err := ensureServePortAvailable(resolvedHostname, port); err == nil {
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available opencode serve port for %s in range %d-%d", resolvedHostname, defaultServePortBase, defaultServePortBase+defaultServePortSpan-1)
}

func deterministicServePortStart(request contracts.TaskSessionStartRequest) int {
	hasher := fnv.New32a()
	for _, part := range []string{
		strings.TrimSpace(request.Backend),
		strings.TrimSpace(request.RepoRoot),
		strings.TrimSpace(request.TaskID),
		strings.TrimSpace(request.LogPath),
	} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{'\n'})
	}
	return defaultServePortBase + int(hasher.Sum32()%defaultServePortSpan)
}

func ensureServePortAvailable(hostname string, port int) error {
	listener, err := net.Listen("tcp", net.JoinHostPort(hostname, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return listener.Close()
}
