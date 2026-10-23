package cmd

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/dao"
	"github.com/msvens/mfide/internal/fide"
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
		url := fide.CurrentURL(config.FideBaseURL())
		t := fide.Target{
			Kind:    dao.SourceCurrent,
			Url:     url,
			DestRel: path.Join("downloads", "current", time.Now().Format(time.DateOnly), path.Base(url)),
		}
		out, f, err := fide.Download(fide.NewFetcher(config.ServiceRoot()), pg.SourceFile, t)
		if err != nil {
			return err
		}
		printOutcome(path.Base(url), out, f)
		return nil
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
		from, err := time.Parse("2006-01", archiveFrom)
		if err != nil {
			return fmt.Errorf("--from must look like 2026-09: %w", err)
		}
		to := time.Now().UTC()
		if archiveTo != "" {
			if to, err = time.Parse("2006-01", archiveTo); err != nil {
				return fmt.Errorf("--to must look like 2026-10: %w", err)
			}
		}
		to = time.Date(to.Year(), to.Month(), 1, 0, 0, 0, 0, time.UTC)
		if to.Before(from) {
			return fmt.Errorf("--to %s is before --from %s", archiveTo, archiveFrom)
		}

		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		delay := archiveDelay
		if delay == 0 {
			delay = config.FideFetchDelay()
		}
		f := fide.NewFetcher(config.ServiceRoot())
		base := config.FideBaseURL()

		// FIDE keeps regenerating a month's archive file until the month is over
		// (October's changed on 22 Oct; September's last on 29 Sep), so the
		// current month is always rechecked.
		now := time.Now().UTC()
		thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

		var failed []string
		requested := false
		for p := from; !p.After(to); p = p.AddDate(0, 1, 0) {
			for _, typ := range types {
				url := fide.ArchiveURL(base, typ, p)
				name := path.Base(url)
				if !archiveRecheck && p.Before(thisMonth) {
					if _, err := pg.SourceFile.Latest(url); err == nil {
						fmt.Printf("%-28s skipped (already have it)\n", name)
						continue
					} else if !errors.Is(err, sql.ErrNoRows) {
						return err
					}
				}
				if requested {
					time.Sleep(delay) // be polite: one file at a time, with a pause
				}
				requested = true
				period, ratingType := p, typ
				out, file, err := fide.Download(f, pg.SourceFile, fide.Target{
					Kind:       dao.SourceArchive,
					RatingType: &ratingType,
					Period:     &period,
					Url:        url,
					DestRel:    path.Join("downloads", "archive", p.Format("2006-01"), name),
				})
				if err != nil {
					fmt.Printf("%-28s FAILED: %v\n", name, err)
					failed = append(failed, name)
					continue
				}
				printOutcome(name, out, file)
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("%d file(s) failed: %v", len(failed), failed)
		}
		return nil
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
		_, _ = fmt.Fprintln(w, "ID\tKIND\tTYPE\tPERIOD\tSIZE\tSHA\tLAST-MODIFIED (FIDE)\tFETCHED\tCHECKED\tPATH")
		for _, f := range files {
			typ, period := "-", "-"
			if f.RatingType != nil {
				typ = *f.RatingType
			}
			if f.Period != nil {
				period = f.Period.Format("2006-01")
			}
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f.Id, f.Kind, typ, period,
				fmtSize(f.Size), f.Sha256[:8], fmtTime(f.LastModified),
				f.FetchedAt.Format(time.DateTime), f.CheckedAt.Format(time.DateTime), f.Path)
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
