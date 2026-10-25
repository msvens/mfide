package cmd

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/dao"
	"github.com/spf13/cobra"
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage API keys",
}

var (
	keyName    string
	keyRate    int
	keyListAll bool
)

var keyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an API key and print it once",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		rate := keyRate
		if rate == 0 {
			rate = config.ApiDefaultRatePerMin()
		}
		plain, k, err := pg.ApiKey.Create(keyName, rate)
		if err != nil {
			return err
		}
		fmt.Printf("Created key %d (%s, %d requests/min).\n", k.Id, k.Name, k.RatePerMin)
		fmt.Printf("Store it now; it can't be shown again:\n\n  %s\n\n", plain)
		return nil
	},
}

var keyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active API keys (--all includes revoked ones)",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		keys, err := pg.ApiKey.List()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tNAME\tPREFIX\tRATE/MIN\tCREATED\tLAST USED\tREVOKED")
		hidden := 0
		for _, k := range keys {
			if k.RevokedAt != nil && !keyListAll {
				hidden++
				continue
			}
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\t%s\n", k.Id, k.Name, k.Prefix, k.RatePerMin,
				k.CreatedAt.Format(time.DateTime), fmtTime(k.LastUsedAt), fmtTime(k.RevokedAt))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if hidden > 0 {
			fmt.Printf("\n%d revoked keys not shown (--all shows them).\n", hidden)
		}
		return nil
	},
}

var keyRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke an API key (a running server stops accepting it within api.keyCacheTTL)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("key id must be a number: %q", args[0])
		}
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		if err := pg.ApiKey.Revoke(id); err != nil {
			return err
		}
		fmt.Printf("Revoked key %d.\n", id)
		return nil
	},
}

func openDB() (*dao.PGDB, error) {
	if err := config.InitConfig(); err != nil {
		return nil, fmt.Errorf("init config: %w", err)
	}
	return dao.NewPGDB()
}

// fmtTime prints in local time; times come back from Postgres and HTTP headers
// in different zones.
func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format(time.DateTime)
}

func init() {
	keyCreateCmd.Flags().StringVar(&keyName, "name", "", "who or what the key is for (required)")
	keyCreateCmd.Flags().IntVar(&keyRate, "rate", 0, "requests per minute (default api.defaultRatePerMin)")
	_ = keyCreateCmd.MarkFlagRequired("name")
	keyListCmd.Flags().BoolVar(&keyListAll, "all", false, "include revoked keys")
	keyCmd.AddCommand(keyCreateCmd, keyListCmd, keyRevokeCmd)
	rootCmd.AddCommand(keyCmd)
}
