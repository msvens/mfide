package cmd

import (
	"fmt"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/dao"
	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database commands",
}

var dbCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check that the configured database is reachable",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.InitConfig(); err != nil {
			return fmt.Errorf("init config: %w", err)
		}
		pg, err := dao.NewPGDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		fmt.Println("database ok")
		return nil
	},
}

func init() {
	dbCmd.AddCommand(dbCheckCmd)
	rootCmd.AddCommand(dbCmd)
}
