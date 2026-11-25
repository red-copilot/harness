package engine

import (
	"github.com/red-copilot/harness/core"
)

func redactorForSpec(spec RunSpec, secrets SecretSource) *core.Redactor {
	values := append([]string(nil), spec.KnownSecrets...)
	agents := make([]AgentSpec, 0, 2)
	agents = append(agents, spec.Reasoner)
	agents = append(agents, spec.Executor)
	var lookup func(string) (string, bool)
	if secrets != nil {
		lookup = secrets.LookupEnv
	}
	for _, key := range spec.SecretEnv {
		if lookup != nil {
			if value, ok := lookup(key); ok {
				values = append(values, value)
			}
		}
		for _, agent := range agents {
			if value, ok := agent.Env[key]; ok {
				values = append(values, value)
			}
		}
	}
	return core.NewRedactor(values...)
}
