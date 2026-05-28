package session

import (
	"os"
	"os/user"
	"regexp"
	"strings"
)

var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// names we manage ourselves; the client cannot override them via "env" reqs,
// and they are stripped from the inherited parent env too — either because we
// re-derive them (PATH/HOME/USER/LOGNAME/SHELL) or because exposing the
// parent's value would bypass server policy (SSH_AUTH_SOCK/SSH_AGENT_PID:
// otherwise the parent's agent socket leaks to children even when the client
// did not request agent forwarding or the server was started with
// --no-agent-forward).
var envBlocklist = map[string]struct{}{
	"PATH":          {},
	"HOME":          {},
	"USER":          {},
	"LOGNAME":       {},
	"SHELL":         {},
	"SSH_AUTH_SOCK": {},
	"SSH_AGENT_PID": {},
}

func envNameAllowed(name string) bool {
	if !envNameRE.MatchString(name) {
		return false
	}
	if strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") {
		return false
	}
	_, blocked := envBlocklist[name]
	return !blocked
}

// composeEnv merges, in order: process env (sans blocklisted names), uid-
// derived identity vars, a sane PATH if missing, client-supplied env (already
// filtered).
func composeEnv(client map[string]string, shell string) []string {
	merged := make(map[string]string, 64)

	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k := kv[:i]
		if _, blocked := envBlocklist[k]; blocked {
			continue
		}
		merged[k] = kv[i+1:]
	}

	if u, err := user.Current(); err == nil {
		merged["USER"] = u.Username
		merged["LOGNAME"] = u.Username
		if u.HomeDir != "" {
			merged["HOME"] = u.HomeDir
		}
	}
	if _, ok := merged["HOME"]; !ok {
		if h := os.Getenv("HOME"); h != "" {
			merged["HOME"] = h
		}
	}
	merged["SHELL"] = shell
	if _, ok := merged["PATH"]; !ok {
		merged["PATH"] = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}

	for k, v := range client {
		merged[k] = v
	}

	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}
