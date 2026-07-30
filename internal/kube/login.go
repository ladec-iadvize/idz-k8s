package kube

// Startup credential preflight (owner request 2026-07-30): EKS clusters
// authenticate through the kubeconfig's exec plugin (aws eks get-token) and
// the day's SSO session expires — the TUI then opened on a broken, empty
// screen. main.go pings the cluster first; when the failure looks like an
// auth problem, it runs the (derived or configured) `aws sso login …`
// command interactively and retries before starting the UI.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
)

// Ping makes the cheapest authenticated call (GET /version) so credential
// problems surface BEFORE the TUI takes over the terminal.
func (c *Client) Ping(ctx context.Context) error {
	res := c.Clientset.CoreV1().RESTClient().Get().AbsPath("/version").Do(ctx)
	return res.Error()
}

// authErrHints are lowercase fragments of the errors client-go surfaces when
// the exec credential plugin (aws eks get-token) cannot mint a token.
var authErrHints = []string{
	"sso",               // "the SSO session has expired", "retrieving token from sso"
	"token has expired", // aws sdk refresh failure
	"expiredtoken",
	"invalidgrantexception",
	"getting credentials", // client-go exec plugin wrapper
	"exec plugin",
	"no valid credential",
	"unauthorized", // EKS 401 on a stale/foreign token
}

// IsAuthError reports whether an API error smells like a credential problem
// (as opposed to an unreachable cluster — a VPN cut must NOT trigger a
// browser login).
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, h := range authErrHints {
		if strings.Contains(msg, h) {
			return true
		}
	}
	return false
}

// LoginCommand derives the interactive re-login command for the ACTIVE
// context from the kubeconfig's exec plugin: the AWS profile comes from the
// exec env (AWS_PROFILE) or args (--profile), and the profile's sso_session
// from the AWS config file. Returns "" when the context does not
// authenticate through an AWS profile (nothing to derive — the configurable
// loginCommand takes over).
func LoginCommand(kubeconfigPath, contextName string) string {
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		loading.ExplicitPath = kubeconfigPath
	}
	raw, err := loading.Load()
	if err != nil {
		return ""
	}
	if contextName == "" {
		contextName = raw.CurrentContext
	}
	ctx, ok := raw.Contexts[contextName]
	if !ok {
		return ""
	}
	auth, ok := raw.AuthInfos[ctx.AuthInfo]
	if !ok || auth.Exec == nil {
		return ""
	}
	profile := ""
	for _, e := range auth.Exec.Env {
		if e.Name == "AWS_PROFILE" || e.Name == "AWS_DEFAULT_PROFILE" {
			profile = e.Value
		}
	}
	for i, a := range auth.Exec.Args {
		if a == "--profile" && i+1 < len(auth.Exec.Args) {
			profile = auth.Exec.Args[i+1]
		}
	}
	if profile == "" {
		profile = os.Getenv("AWS_PROFILE")
	}
	if profile == "" {
		return ""
	}
	if session := ssoSessionOf(profile); session != "" {
		// One login covers every profile of the session (all the contexts).
		return fmt.Sprintf("aws sso login --sso-session %s", session)
	}
	return fmt.Sprintf("aws sso login --profile %s", profile)
}

// ssoSessionOf reads the profile's sso_session from the AWS config file
// (minimal INI walk — no AWS SDK dependency for one key).
func ssoSessionOf(profile string) string {
	path := os.Getenv("AWS_CONFIG_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, ".aws", "config")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inProfile := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inProfile = line == "[profile "+profile+"]" || (profile == "default" && line == "[default]")
			continue
		}
		if !inProfile {
			continue
		}
		if k, v, found := strings.Cut(line, "="); found && strings.TrimSpace(k) == "sso_session" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
