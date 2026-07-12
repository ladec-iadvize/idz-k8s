// Command idz-k8s is a strictly read-only Kubernetes overview/debug TUI.
// It never mutates cluster state; administration is done in a separate tool.
package main

import (
	"fmt"
	"os"

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
	)

	root := &cobra.Command{
		Use:   "idz-k8s",
		Short: "Read-only Kubernetes overview & debugging TUI",
		Long:  "idz-k8s is a strictly read-only terminal client to browse, inspect and debug a Kubernetes cluster. It performs no mutating action.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Println("idz-k8s", version)
				return nil
			}
			if configPath == "" {
				configPath = config.DefaultPath()
			}
			log := telemetry.New(os.Stderr, false)

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
			_ = noColor // lipgloss honors NO_COLOR; flag reserved for explicit override
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

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
