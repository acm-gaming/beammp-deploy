package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/acm-gaming/beammp-deploy/internal/config"
	"github.com/acm-gaming/beammp-deploy/internal/deploy"
	"github.com/acm-gaming/beammp-deploy/internal/logging"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	configPath string
	verbose    bool
	noTUI      bool
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

		cfg, cfgPathUsed, err := config.Load(configPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if shouldUseDeployTUI(cmd) {
			return runDeployTUI(cmd, ctx, cfg, cfgPathUsed)
		}
		return runDeployPlain(cmd, ctx, cfg, cfgPathUsed)
	},
}

func runDeployPlain(cmd *cobra.Command, ctx context.Context, cfg *config.Config, cfgPathUsed string) error {
	logger, err := logging.New(verbose)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	deployer, err := deploy.New(logger, cfg, cfgPathUsed)
	if err != nil {
		return fmt.Errorf("build deployer: %w", err)
	}

	if err := deployer.Run(ctx, servers); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Deployment complete for %d server(s)\n", deployer.ExecutedServerCount())
	return nil
}

func runDeployTUI(cmd *cobra.Command, parentCtx context.Context, cfg *config.Config, cfgPathUsed string) error {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	program, logWriter, observer := newDeployProgram(cfgPathUsed, cmd.InOrStdin(), cmd.OutOrStdout(), cancel)

	logger, err := logging.NewWithWriter(verbose, logWriter)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() { _ = logger.Sync() }()

	deployer, err := deploy.New(logger, cfg, cfgPathUsed)
	if err != nil {
		return fmt.Errorf("build deployer: %w", err)
	}
	deployer.SetObserver(observer)

	doneCh := make(chan deployDoneMsg, 1)
	go func() {
		result := deployDoneMsg{
			err:      deployer.Run(ctx, servers),
			executed: deployer.ExecutedServerCount(),
		}
		doneCh <- result
		logWriter.Flush()
		program.Send(result)
	}()

	if _, err := program.Run(); err != nil {
		cancel()
		result := <-doneCh
		if result.err != nil {
			return result.err
		}
		return err
	}

	result := <-doneCh
	if result.err != nil {
		return result.err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Deployment complete for %d server(s)\n", result.executed)
	return nil
}

func shouldUseDeployTUI(cmd *cobra.Command) bool {
	if noTUI {
		return false
	}
	return isTerminalIn(cmd.InOrStdin()) && isTerminalOut(cmd.OutOrStdout())
}

func isTerminalIn(stream io.Reader) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func isTerminalOut(stream io.Writer) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "Path to config TOML (defaults to XDG config location)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logs")
	rootCmd.PersistentFlags().BoolVar(&noTUI, "no-tui", false, "Disable deploy TUI and print logs directly")
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
