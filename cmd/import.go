package cmd

import (
	"fmt"
	"os"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/fide"
	"github.com/msvens/mfide/internal/syncer"
	"github.com/spf13/cobra"
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import downloaded FIDE files into the database",
}

var (
	importFile  int
	importForce bool
)

var importCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Import the latest downloaded current list (or --file <id> from `fetch list`)",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()

		file, err := syncer.LatestCurrent(pg, importFile)
		if err != nil {
			return err
		}
		if !importForce {
			done, err := pg.ImportRun.Imported(file.Id)
			if err != nil {
				return err
			}
			if done {
				fmt.Printf("file %d (%s) is already imported; use --force to import it again\n", file.Id, file.Path)
				return nil
			}
		}

		fmt.Printf("importing file %d (%s, FIDE last-modified %s)\n", file.Id, file.Path, fmtTime(file.LastModified))
		start := time.Now()
		res, err := syncer.ImportCurrentFile(pg, config.ServiceRoot(), file)
		if err != nil {
			return err
		}
		fmt.Printf("%d players: %d inserted, %d updated, %d removed in %s\n",
			res.Players, res.Inserted, res.Updated, res.Removed, time.Since(start).Round(100*time.Millisecond))
		fmt.Printf("changes among existing players: profile %d, returned %d\n",
			res.Changes["profile"], res.Changes["returned"])
		return nil
	},
}

var (
	importArchiveType string
	importArchiveFrom string
	importArchiveTo   string
)

var importArchiveCmd = &cobra.Command{
	Use:   "archive",
	Short: "Import downloaded monthly archive files into the rating history",
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := monthRange(importArchiveFrom, importArchiveTo)
		if err != nil {
			return err
		}
		if importArchiveType != "all" {
			if !slices.Contains(fide.RatingTypes, importArchiveType) {
				return fmt.Errorf("--type must be standard, rapid, blitz or all, got %q", importArchiveType)
			}
		}
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		files, err := syncer.LatestArchives(pg, from, to, importArchiveType)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("no downloaded archive files on disk in that range; run `fetch archive` first")
		}
		imported := 0
		for _, f := range files {
			key := syncer.ArchiveKey(f)
			if !importForce {
				done, err := pg.ImportRun.Imported(f.Id)
				if err != nil {
					return err
				}
				if done {
					fmt.Printf("%-16s skipped (already imported)\n", key)
					continue
				}
			}
			start := time.Now()
			res, err := syncer.ImportArchiveFile(pg, config.ServiceRoot(), f)
			if err != nil {
				return err
			}
			fmt.Printf("%-16s %7d players, %d new or changed, %d dropped, in %s\n",
				key, res.Players, res.Updated, res.Removed, time.Since(start).Round(100*time.Millisecond))
			imported++
		}
		if imported > 0 {
			start := time.Now()
			if err := pg.VacuumRating(); err != nil {
				return fmt.Errorf("vacuum rating: %w", err)
			}
			fmt.Printf("vacuumed rating in %s\n", time.Since(start).Round(100*time.Millisecond))
		}
		return nil
	},
}

// monthRange parses --from/--to as months; --to defaults to this month.
func monthRange(fromS, toS string) (time.Time, time.Time, error) {
	from, err := time.Parse("2006-01", fromS)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--from must look like 2026-05: %w", err)
	}
	now := time.Now().UTC()
	to := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if toS != "" {
		if to, err = time.Parse("2006-01", toS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to must look like 2026-10: %w", err)
		}
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("--to %s is before --from %s", toS, fromS)
	}
	return from, to, nil
}

var importRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List past imports",
	RunE: func(cmd *cobra.Command, args []string) error {
		pg, err := openDB()
		if err != nil {
			return err
		}
		defer func() { _ = pg.Close() }()
		runs, err := pg.ImportRun.List()
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tFILE\tFINISHED\tDURATION\tPLAYERS\tINSERTED\tUPDATED\tREMOVED\tCHANGES")
		for _, r := range runs {
			_, _ = fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n", r.Id, r.SourceFileId,
				r.FinishedAt.Local().Format(time.DateTime), r.FinishedAt.Sub(r.StartedAt).Round(time.Second),
				r.Players, r.Inserted, r.Updated, r.Removed, r.Changes)
		}
		return w.Flush()
	},
}

func init() {
	importCurrentCmd.Flags().IntVar(&importFile, "file", 0, "source file id (default: the latest current list)")
	importCurrentCmd.Flags().BoolVar(&importForce, "force", false, "import even if this file was imported before")
	importArchiveCmd.Flags().StringVar(&importArchiveType, "type", "all", "standard, rapid, blitz or all")
	importArchiveCmd.Flags().StringVar(&importArchiveFrom, "from", "", "first month, e.g. 2026-05 (required)")
	importArchiveCmd.Flags().StringVar(&importArchiveTo, "to", "", "last month (default: this month)")
	importArchiveCmd.Flags().BoolVar(&importForce, "force", false, "import even if a file was imported before")
	_ = importArchiveCmd.MarkFlagRequired("from")
	importCmd.AddCommand(importCurrentCmd, importArchiveCmd, importRunsCmd)
	rootCmd.AddCommand(importCmd)
}
