package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/acm-gaming/beammp-deploy/internal/config"
	"github.com/acm-gaming/beammp-deploy/internal/deploy"
	"github.com/acm-gaming/beammp-deploy/internal/logging"
	"github.com/spf13/cobra"
)

var (
	configPath string
	verbose    bool
	servers    []string
)

var rootCmd = &cobra.Command{
	Use:   "beammp-deploy",
	Short: "Deploy BeamMP modules to remote servers",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		logger, err := logging.New(verbose)
		if err != nil {
			return fmt.Errorf("init logger: %w", err)
		}
		defer func() { _ = logger.Sync() }()

		cfg, cfgPathUsed, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		deployer, err := deploy.New(logger, cfg, cfgPathUsed)
		if err != nil {
			return fmt.Errorf("build deployer: %w", err)
		}

		if err := deployer.Run(ctx, servers); err != nil {
			return err
		}

		fmt.Printf("Deployment complete for %d server(s)\n", deployer.ExecutedServerCount())
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Path to config TOML (defaults to XDG config location)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logs")
	rootCmd.PersistentFlags().StringSliceVar(&servers, "server", nil, "Server name to deploy (repeatable)")

	rootCmd.SetContext(context.Background())
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true

	addConfigCommands(rootCmd)
}

func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Println("Canceled")
			return nil
		}
		return err
	}
	return nil
}
