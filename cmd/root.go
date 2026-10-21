package cmd

import (
	"fmt"
	"os"

	"github.com/msvens/mfide/internal/server"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mfide",
	Short: "mfide serves FIDE rating data",
	Long: `mfide imports FIDE's downloadable rating lists into Postgres and serves them over
a JSON API. Without a subcommand it starts the server.`,
	Run: func(cmd *cobra.Command, args []string) { server.StartMServer() },
	// Execute prints the error once; usage would only bury it.
	SilenceErrors: true,
	SilenceUsage:  true,
}

// Execute runs the root command; called by main.main().
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
