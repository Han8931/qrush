package server

import (
	"strings"
	"sync"
)

type EnvStore struct {
	mu   sync.RWMutex
	vars map[string]string
}

func NewEnvStore() *EnvStore {
	return &EnvStore{vars: make(map[string]string)}
}

func (e *EnvStore) Get(key string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.vars[key]
	return v, ok
}

func (e *EnvStore) Set(key, value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.vars[key] = value
}

func (e *EnvStore) Unset(key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.vars, key)
}

func (e *EnvStore) Apply(env []string) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.vars) == 0 {
		return env
	}

	out := make([]string, 0, len(env)+len(e.vars))
	seen := make(map[string]bool, len(env)+len(e.vars))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		if value, override := e.vars[key]; override {
			out = append(out, key+"="+value)
			seen[key] = true
			continue
		}
		out = append(out, entry)
		seen[key] = true
	}
	for key, value := range e.vars {
		if !seen[key] {
			out = append(out, key+"="+value)
		}
	}
	return out
}
