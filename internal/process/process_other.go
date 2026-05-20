//go:build !js

package process

import (
	"fmt"
	"os"
	"os/exec"
)

func (p *process) run(path string) {
	cmd := exec.Command(path, p.args...)
	if p.env == nil {
		cmd.Env = os.Environ()
		p.env = splitEnvPairs(cmd.Env)
	} else {
		for k, v := range p.env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	p.state = stateRunning
	prev := switchContext(p.pid)
	err := cmd.Run()
	switchContext(prev)
	p.exitCode = cmd.ProcessState.ExitCode()
	p.handleErr(err)
}
