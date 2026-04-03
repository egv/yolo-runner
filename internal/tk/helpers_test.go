package tk

import "strings"

type fakeRunner struct {
	responses map[string]string
	calls     []string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, joined)
	if out, ok := f.responses[joined]; ok {
		return out, nil
	}
	if len(args) >= 2 {
		if out, ok := f.responses[args[0]+" "+args[1]]; ok {
			return out, nil
		}
	}
	return "", nil
}

func (f *fakeRunner) called(cmd string) bool {
	for _, call := range f.calls {
		if call == cmd {
			return true
		}
	}
	return false
}
