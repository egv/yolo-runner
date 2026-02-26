package distributed

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func DetectEnvironmentFeatureProbes() ExecutorEnvironmentFeatureProbes {
	return ExecutorEnvironmentFeatureProbes{
		HasGo:     commandAvailable("go"),
		HasGit:    commandAvailable("git"),
		HasDocker: commandAvailable("docker"),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

func DetectResourceHints() ExecutorResourceHints {
	return ExecutorResourceHints{
		CPUCores: runtime.NumCPU(),
		MemGB:    detectMemGB(),
	}
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(strings.TrimSpace(name))
	return err == nil
}

func detectMemGB() float64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		valueKB, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || valueKB <= 0 {
			return 0
		}
		return valueKB / (1024 * 1024)
	}
	return 0
}
