package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/msvens/mfide/internal/syncer"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Fetch and import what FIDE changed, then apply the retention rules (what the service runs daily)",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		// Ctrl-C stops at the next step; an import in progress finishes or rolls back.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		s, err := syncer.Run(ctx, pg, syncer.OptionsFromConfig(), func(line string) { fmt.Println(line) })
		if err != nil {
			return err
		}
		if s.AlreadyRunning {
			fmt.Println("another sync is running (the service's scheduler?); nothing done")
			return nil
		}
		if s.Failed {
			return errors.New("sync finished with failures (see above)")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
