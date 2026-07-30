// Command idz-k8s is a Kubernetes overview, debugging and administration
// TUI. Every mutating action goes through an explicit confirmation step.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/telemetry"
	"github.com/iadvize/idz-k8s/internal/ui"
	"github.com/iadvize/idz-k8s/internal/ui/theme"
)

var version = "0.1.0-dev"

func main() {
	var (
		kubeconfig    string
		contextName   string
		namespace     string
		configPath    string
		prometheusURL string
		refresh       int
		noMouse       bool
		noColor       bool
		themeFlag     string
		kikoo         bool
		showVersion   bool
		loginCmd      string
		noLogin       bool
	)

	root := &cobra.Command{
		Use:   "idz-k8s",
		Short: "Kubernetes overview, debugging & admin TUI",
		Long:  "idz-k8s is a terminal client to browse, inspect, debug and administer a Kubernetes cluster. Admin actions (edit, scale, delete, port-forward…) always ask for confirmation before touching the cluster.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Println("idz-k8s", version)
				return nil
			}
			if configPath == "" {
				configPath = config.DefaultPath()
			}
			log := telemetry.New(os.Stderr)

			cfg, err := config.Load(configPath)
			if err != nil {
				log.Warn("config load failed, using defaults", "err", err)
			}
			if prometheusURL != "" {
				cfg.PrometheusURL = prometheusURL
			}
			if refresh > 0 {
				cfg.RefreshIntervalSeconds = refresh
			}

			// Restore last-used context/namespace unless overridden by flags.
			ctxToUse := contextName
			if ctxToUse == "" {
				ctxToUse = cfg.LastContext
			}
			nsToUse := namespace
			if nsToUse == "" {
				nsToUse = cfg.LastNamespace
			}
			client, err := kube.NewClient(kube.Options{
				KubeconfigPath: kubeconfig,
				Context:        ctxToUse,
				Namespace:      nsToUse,
			})
			if err != nil && contextName == "" && cfg.LastContext != "" {
				// Remembered context may no longer exist; fall back to default.
				// Reset ctxToUse too: helm.New below must target the context
				// actually in use, not the dead remembered one.
				log.Warn("remembered context unavailable, using kubeconfig default", "context", cfg.LastContext, "err", err)
				ctxToUse = ""
				client, err = kube.NewClient(kube.Options{KubeconfigPath: kubeconfig, Namespace: namespace})
			}
			if err != nil {
				return fmt.Errorf("connecting to cluster: %w", err)
			}

			// Credential preflight (owner request 2026-07-30): when the day's
			// AWS SSO session expired, run the login interactively NOW —
			// before the TUI owns the terminal — instead of opening a broken
			// empty screen. Network problems (VPN off) are not auth problems
			// and skip this entirely.
			if !noLogin {
				cmdStr := loginCmd
				if cmdStr == "" {
					cmdStr = cfg.LoginCommand
				}
				if renewed, rerr := ensureCredentials(client, cmdStr, kubeconfig, ctxToUse); rerr != nil {
					log.Warn("credential preflight", "err", rerr)
				} else if renewed != nil {
					client.Close()
					client = renewed
				}
			}

			mc, err := metrics.NewClient(cfg.PrometheusURL)
			if err != nil {
				log.Warn("prometheus client init failed; metrics will show unavailable", "err", err)
			}

			if themeFlag != "" {
				cfg.Theme = themeFlag // session override; persisted prefs untouched
			}
			m := ui.New(client, cfg, kubeconfig,
				ui.WithMetrics(mc),
				ui.WithHelm(helm.New(kubeconfig, ctxToUse)),
				ui.WithConfigPath(configPath),
				ui.WithInitialTypeKey(cfg.LastType),
				ui.WithMouse(!noMouse),
				ui.WithTheme(theme.ForName(cfg.Theme)),
				ui.WithKikoo(kikoo),
			)
			opts := []tea.ProgramOption{tea.WithAltScreen()}
			if !noMouse {
				opts = append(opts, tea.WithMouseCellMotion())
			}
			if noColor {
				// Same path as the NO_COLOR env convention — lipgloss reads it
				// on first render; symbols carry meaning without color (FR-020).
				_ = os.Setenv("NO_COLOR", "1")
			}
			p := tea.NewProgram(m, opts...)
			_, err = p.Run()
			return err
		},
	}

	f := root.Flags()
	f.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: standard loading rules)")
	f.StringVar(&contextName, "context", "", "kubeconfig context to use (default: current-context)")
	f.StringVarP(&namespace, "namespace", "n", "", "starting namespace")
	f.StringVar(&configPath, "config", "", "preferences file (default: XDG config dir)")
	f.StringVar(&prometheusURL, "prometheus-url", "", "Prometheus endpoint (single metrics source)")
	f.IntVar(&refresh, "refresh", 0, "refresh interval in seconds (default: config or 5)")
	f.BoolVar(&noMouse, "no-mouse", false, "disable mouse capture (keyboard-only)")
	f.BoolVar(&noColor, "no-color", false, "force plain rendering (also honors NO_COLOR)")
	f.StringVar(&themeFlag, "theme", "", "theme: auto (follows the terminal background), dark, light")
	f.BoolVar(&showVersion, "version", false, "print version and exit")
	f.BoolVar(&kikoo, "kikoo", false, "celebratory ASCII banner (iAdvize green)")
	f.StringVar(&loginCmd, "login-cmd", "", "command run when credentials are expired (default: derived from the kubeconfig's AWS profile, e.g. 'aws sso login --sso-session …')")
	f.BoolVar(&noLogin, "no-login", false, "never run a login command automatically")

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// ensureCredentials pings the cluster and, on an auth-shaped failure, runs
// the login command interactively (browser flow and all), then returns a
// FRESH client (the old one may cache the failed exec credentials).
// (nil, nil) = credentials were already fine or the failure is not
// auth-related (the TUI's own unreachable banner handles it).
func ensureCredentials(client *kube.Client, loginCmd, kubeconfigPath, contextName string) (*kube.Client, error) {
	ping := func(c *kube.Client) error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return c.Ping(ctx)
	}
	err := ping(client)
	if err == nil || !kube.IsAuthError(err) {
		return nil, nil
	}
	if loginCmd == "" {
		loginCmd = kube.LoginCommand(kubeconfigPath, contextName)
	}
	if loginCmd == "" {
		return nil, fmt.Errorf("credentials rejected (%v) and no login command available — set loginCommand in the config or pass --login-cmd", err)
	}
	fmt.Fprintf(os.Stderr, "⚠ cluster credentials expired (%v)\n⏳ running: %s\n", err, loginCmd)
	login := exec.Command("sh", "-c", loginCmd)
	login.Stdin, login.Stdout, login.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := login.Run(); err != nil {
		return nil, fmt.Errorf("%q failed: %w", loginCmd, err)
	}
	// Fresh client: the exec credential plugin result is cached per client.
	renewed, err := kube.NewClient(kube.Options{
		KubeconfigPath: kubeconfigPath, Context: contextName, Namespace: client.Namespace,
	})
	if err != nil {
		return nil, err
	}
	if err := ping(renewed); err != nil {
		renewed.Close()
		return nil, fmt.Errorf("still failing after login: %w", err)
	}
	fmt.Fprintln(os.Stderr, "✓ credentials renewed")
	return renewed, nil
}
