package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

var (
	configurationFilePathFlag string

	rootCmd = &cobra.Command{
		Use:   "homebox-analytics",
		Short: "Consumable usage tracking and analytics for Homebox",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(configurationFilePathFlag)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return run(ctx, cfg, version, zap.L())
		},
	}
)

func init() {
	logger, _ := zap.NewProduction()
	zap.ReplaceGlobals(logger)

	rootCmd.PersistentFlags().StringVar(&configurationFilePathFlag, "config", "", "config file path")
}

func main() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		zap.L().Fatal("Unable to run", zap.Error(err))
	}
}
