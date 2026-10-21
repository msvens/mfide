package cmd

import (
	"github.com/msvens/mfide/internal/server"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Start the server",
	Run:   func(cmd *cobra.Command, args []string) { server.StartMServer() },
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
