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

var dbUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Create or migrate the database schema to this binary's version",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.InitConfig(); err != nil {
			return fmt.Errorf("init config: %w", err)
		}
		pg, err := dao.NewPGDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		from, to, err := pg.Upgrade()
		if err != nil {
			return err
		}
		if from == to {
			fmt.Printf("database up to date (version %d)\n", to)
		} else {
			fmt.Printf("database upgraded %d -> %d\n", from, to)
		}
		return nil
	},
}

var dbVersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the database schema version and the version this binary requires",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.InitConfig(); err != nil {
			return fmt.Errorf("init config: %w", err)
		}
		pg, err := dao.NewPGDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		v, err := pg.Version()
		if err != nil {
			return err
		}
		fmt.Printf("database %d, binary %d\n", v, dao.DbVersion)
		return nil
	},
}

func init() {
	dbCmd.AddCommand(dbCheckCmd)
	dbCmd.AddCommand(dbUpgradeCmd)
	dbCmd.AddCommand(dbVersionCmd)
	rootCmd.AddCommand(dbCmd)
}
