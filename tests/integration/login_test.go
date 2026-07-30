package integration

// Startup credential preflight (owner request 2026-07-30): auth-shaped
// failures are told apart from network ones, and the re-login command is
// derived from the kubeconfig's AWS exec profile + the AWS config file.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iadvize/idz-k8s/internal/kube"
)

func TestIsAuthErrorSeparatesAuthFromNetwork(t *testing.T) {
	auth := []string{
		`getting credentials: exec: executable aws failed with exit code 255`,
		`the SSO session associated with this profile has expired or is otherwise invalid`,
		`Error when retrieving token from sso: Token has expired and refresh failed`,
		`InvalidGrantException: invalid_grant`,
		`the server has asked for the client to provide credentials (Unauthorized)`,
		`ExpiredToken: The security token included in the request is expired`,
	}
	for _, msg := range auth {
		if !kube.IsAuthError(errors.New(msg)) {
			t.Fatalf("must be an auth error: %q", msg)
		}
	}
	network := []string{
		`dial tcp 10.0.0.1:443: connect: connection refused`,
		`Get "https://api": context deadline exceeded`,
		`no such host`,
	}
	for _, msg := range network {
		if kube.IsAuthError(errors.New(msg)) {
			t.Fatalf("a network failure must NOT trigger a browser login: %q", msg)
		}
	}
	if kube.IsAuthError(nil) {
		t.Fatal("nil is not an error")
	}
}

// TestLoginCommandDerivedFromKubeconfig: the exec plugin's AWS_PROFILE plus
// the AWS config's sso_session yield the exact command the operator would
// type (aws sso login --sso-session …); a profile without an sso_session
// falls back to --profile; a non-exec context derives nothing.
func TestLoginCommandDerivedFromKubeconfig(t *testing.T) {
	dir := t.TempDir()
	kubeconfig := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(`apiVersion: v1
kind: Config
current-context: prod
contexts:
- name: prod
  context: {cluster: c, user: prod-user}
- name: legacy
  context: {cluster: c, user: legacy-user}
- name: token
  context: {cluster: c, user: token-user}
clusters:
- name: c
  cluster: {server: "https://example"}
users:
- name: prod-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args: [eks, get-token, --cluster-name, prod]
      env:
      - {name: AWS_PROFILE, value: prod}
- name: legacy-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args: [eks, get-token, --profile, legacy]
- name: token-user
  user: {token: abc}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	awsCfg := filepath.Join(dir, "aws-config")
	if err := os.WriteFile(awsCfg, []byte(`[profile prod]
sso_session = iAdvizeSSO
sso_account_id = 123

[sso-session iAdvizeSSO]
sso_start_url = https://example.awsapps.com/start
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", awsCfg)
	t.Setenv("AWS_PROFILE", "")

	if got := kube.LoginCommand(kubeconfig, "prod"); got != "aws sso login --sso-session iAdvizeSSO" {
		t.Fatalf("prod context: %q", got)
	}
	// current-context is used when none is given.
	if got := kube.LoginCommand(kubeconfig, ""); got != "aws sso login --sso-session iAdvizeSSO" {
		t.Fatalf("current-context: %q", got)
	}
	// --profile in args, profile absent from the AWS config → --profile form.
	if got := kube.LoginCommand(kubeconfig, "legacy"); got != "aws sso login --profile legacy" {
		t.Fatalf("legacy context: %q", got)
	}
	// Static token auth: nothing to derive.
	if got := kube.LoginCommand(kubeconfig, "token"); got != "" {
		t.Fatalf("token context must derive nothing, got %q", got)
	}
}
