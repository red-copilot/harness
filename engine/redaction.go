package engine

import (
	"os"

	"github.com/red-copilot/harness/core"
)

func redactorForSpec(spec RunSpec) *core.Redactor {
	values := append([]string(nil), spec.KnownSecrets...)
	agents := make([]AgentSpec, 0, 1+len(spec.Executors))
	agents = append(agents, spec.Reasoner)
	agents = append(agents, spec.Executors...)
	for _, key := range spec.SecretEnv {
		if value, ok := os.LookupEnv(key); ok {
			values = append(values, value)
		}
		for _, agent := range agents {
			if value, ok := agent.Env[key]; ok {
				values = append(values, value)
			}
		}
	}
	return core.NewRedactor(values...)
}
