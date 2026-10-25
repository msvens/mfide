package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/msvens/mfide/internal/dao"
	"github.com/msvens/mfide/internal/fide"
	"github.com/msvens/mfide/internal/syncer"
	"github.com/spf13/cobra"
)

var fetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Download FIDE's rating-list files",
}

var fetchCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Download the combined current list if it changed",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		return writeLocked(pg, func() error {
			f := syncer.FetchCurrent(pg, syncer.OptionsFromConfig())
			if f.Err != nil {
				return f.Err
			}
			printOutcome(f.Name, f.Outcome, f.File)
			return nil
		})
	},
}

var (
	archiveType    string
	archiveFrom    string
	archiveTo      string
	archiveDelay   time.Duration
	archiveRecheck bool
)

var fetchArchiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Download monthly per-type lists, one file at a time",
	RunE: func(cmd *cobra.Command, args []string) error {
		types := fide.RatingTypes
		if archiveType != "all" {
			if !slices.Contains(fide.RatingTypes, archiveType) {
				return fmt.Errorf("--type must be standard, rapid, blitz or all, got %q", archiveType)
			}
			types = []string{archiveType}
		}
		from, to, err := monthRange(archiveFrom, archiveTo)
		if err != nil {
			return err
		}
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		o := syncer.OptionsFromConfig()
		if archiveDelay != 0 {
			o.Delay = archiveDelay
		}
		var months []time.Time
		for p := from; !p.After(to); p = p.AddDate(0, 1, 0) {
			months = append(months, p)
		}
		return writeLocked(pg, func() error {
			var failed []string
			err := syncer.FetchArchive(context.Background(), pg, o, months, types, archiveRecheck, func(f syncer.Fetched) {
				switch {
				case f.Skipped:
					fmt.Printf("%-28s skipped (already have it)\n", f.Name)
				case f.Err != nil:
					fmt.Printf("%-28s FAILED: %v\n", f.Name, f.Err)
					failed = append(failed, f.Name)
				default:
					printOutcome(f.Name, f.Outcome, f.File)
				}
			})
			if err != nil {
				return err
			}
			if len(failed) > 0 {
				return fmt.Errorf("%d file(s) failed: %v", len(failed), failed)
			}
			return nil
		})
	},
}

var fetchListCmd = &cobra.Command{
	Use:   "list",
	Short: "List downloaded files",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		files, err := pg.SourceFile.List("")
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tKIND\tTYPE\tPERIOD\tSIZE\tSHA\tLAST-MODIFIED (FIDE)\tFETCHED\tCHECKED\tDELETED\tPATH")
		for _, f := range files {
			typ, period := "-", "-"
			if f.RatingType != nil {
				typ = *f.RatingType
			}
			if f.Period != nil {
				period = f.Period.Format("2006-01")
			}
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f.Id, f.Kind, typ, period,
				fmtSize(f.Size), f.Sha256[:8], fmtTime(f.LastModified),
				fmtTime(&f.FetchedAt), fmtTime(&f.CheckedAt), fmtTime(f.DeletedAt), f.Path)
		}
		return w.Flush()
	},
}

func printOutcome(name string, out fide.Outcome, f *dao.SourceFile) {
	fmt.Printf("%-28s %-12s %8s  sha %s  last-modified %s\n", name, out, fmtSize(f.Size), f.Sha256[:8], fmtTime(f.LastModified))
}

func fmtSize(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

func init() {
	fetchArchiveCmd.Flags().StringVar(&archiveType, "type", "all", "standard, rapid, blitz or all")
	fetchArchiveCmd.Flags().StringVar(&archiveFrom, "from", "", "first month, e.g. 2026-09 (required)")
	fetchArchiveCmd.Flags().StringVar(&archiveTo, "to", "", "last month (default: this month)")
	fetchArchiveCmd.Flags().DurationVar(&archiveDelay, "delay", 0, "pause between downloads (default fide.fetchDelay)")
	fetchArchiveCmd.Flags().BoolVar(&archiveRecheck, "recheck", false, "re-request files we already have (conditional GET)")
	_ = fetchArchiveCmd.MarkFlagRequired("from")
	fetchCmd.AddCommand(fetchCurrentCmd, fetchArchiveCmd, fetchListCmd)
	rootCmd.AddCommand(fetchCmd)
}
