package session

import (
	"strings"
	"testing"
)

func TestEnvNameAllowed(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"TERM", true},
		{"LANG", true},
		{"LC_ALL", true},
		{"FOO_BAR", true},
		{"_PRIVATE", true},

		{"PATH", false},
		{"HOME", false},
		{"USER", false},
		{"LOGNAME", false},
		{"SHELL", false},

		{"LD_PRELOAD", false},
		{"LD_LIBRARY_PATH", false},
		{"DYLD_INSERT_LIBRARIES", false},

		{"", false},
		{"lowercase", false},
		{"Mixed", false},
		{"1LEADING_DIGIT", false},
		{"WITH-DASH", false},
		{"WITH SPACE", false},
		{"WITH.DOT", false},
	}
	for _, tc := range cases {
		if got := envNameAllowed(tc.name); got != tc.want {
			t.Errorf("envNameAllowed(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestComposeEnvIdentityAndOverrides(t *testing.T) {
	t.Setenv("PATH", "/sbin:/bin")
	t.Setenv("EXISTING", "from-parent")

	client := map[string]string{
		"TERM": "xterm-256color",
		"LANG": "en_US.UTF-8",
	}
	env := composeEnv(client, "/usr/bin/zsh")
	got := toMap(env)

	if got["SHELL"] != "/usr/bin/zsh" {
		t.Errorf("SHELL = %q, want /usr/bin/zsh", got["SHELL"])
	}
	if got["PATH"] == "" {
		t.Error("PATH not set")
	}
	if got["TERM"] != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", got["TERM"])
	}
	if got["LANG"] != "en_US.UTF-8" {
		t.Errorf("LANG = %q, want en_US.UTF-8", got["LANG"])
	}
	if got["EXISTING"] != "from-parent" {
		t.Errorf("EXISTING = %q, want from-parent (passthrough)", got["EXISTING"])
	}
	if _, ok := got["USER"]; !ok {
		t.Error("USER missing")
	}
	if _, ok := got["HOME"]; !ok {
		t.Error("HOME missing")
	}
}

// composeEnv passes the parent process env through as-is (modulo the names
// we manage ourselves). The LD_*/DYLD_* filter applies only to client-sent
// env requests via envNameAllowed.
func TestComposeEnvPassesParentLDVars(t *testing.T) {
	t.Setenv("LD_PRELOAD", "from-parent")
	env := composeEnv(nil, "/bin/sh")
	if got := toMap(env)["LD_PRELOAD"]; got != "from-parent" {
		t.Errorf("LD_PRELOAD = %q, want from-parent (parent env is trusted)", got)
	}
}

// Parent SHELL/PATH/HOME/USER/LOGNAME are dropped because composeEnv
// derives them from the current uid / overrides them itself.
func TestComposeEnvDropsManagedParentNames(t *testing.T) {
	t.Setenv("SHELL", "/bin/fakeparentshell")
	env := composeEnv(nil, "/bin/sh")
	if got := toMap(env)["SHELL"]; got != "/bin/sh" {
		t.Errorf("SHELL = %q, want /bin/sh (parent SHELL must be overridden)", got)
	}
}

func TestComposeEnvDefaultPATHWhenParentMissing(t *testing.T) {
	t.Setenv("PATH", "")
	env := composeEnv(nil, "/bin/sh")
	got := toMap(env)
	if !strings.Contains(got["PATH"], "/usr/bin") {
		t.Errorf("default PATH should contain /usr/bin, got %q", got["PATH"])
	}
}

func TestComposeEnvClientOverridesParent(t *testing.T) {
	t.Setenv("EDITOR", "vi")
	env := composeEnv(map[string]string{"EDITOR": "nano"}, "/bin/sh")
	if got := toMap(env)["EDITOR"]; got != "nano" {
		t.Errorf("EDITOR = %q, want nano (client wins)", got)
	}
}

func toMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out
}
