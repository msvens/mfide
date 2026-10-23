package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/msvens/mfide/internal/config"
	"github.com/msvens/mfide/internal/dao"
	"github.com/msvens/mfide/internal/fide"
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

		file, err := currentFile(pg, importFile)
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

		r, err := fide.OpenZip(filepath.Join(config.ServiceRoot(), file.Path))
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()

		fmt.Printf("importing file %d (%s, FIDE last-modified %s)\n", file.Id, file.Path, fmtTime(file.LastModified))
		start := time.Now()
		res, err := pg.ImportCurrent(file.Id, r)
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

// currentFile is the source file with that id, or the latest current one.
func currentFile(pg *dao.PGDB, id int) (*dao.SourceFile, error) {
	files, err := pg.SourceFile.List(dao.SourceCurrent)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no current list downloaded yet; run `fetch current` first")
	}
	if id == 0 {
		return &files[len(files)-1], nil // List is oldest first
	}
	for i := range files {
		if files[i].Id == id {
			return &files[i], nil
		}
	}
	return nil, fmt.Errorf("no current-list file with id %d (see `fetch list`)", id)
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
		files, err := pg.SourceFile.List(dao.SourceArchive)
		if err != nil {
			return err
		}
		// Latest download per (type, month): a regenerated file supersedes the older one.
		latest := map[string]*dao.SourceFile{}
		var order []string
		for i := range files {
			f := &files[i]
			if f.Period.Before(from) || f.Period.After(to) || (importArchiveType != "all" && *f.RatingType != importArchiveType) {
				continue
			}
			key := f.Period.Format("2006-01") + " " + *f.RatingType
			if _, seen := latest[key]; !seen {
				order = append(order, key)
			}
			latest[key] = f // List is ordered by fetched_at within (period, type)
		}
		if len(order) == 0 {
			return fmt.Errorf("no downloaded archive files in that range; run `fetch archive` first")
		}
		imported := 0
		for _, key := range order {
			f := latest[key]
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
			r, err := fide.OpenZip(filepath.Join(config.ServiceRoot(), f.Path))
			if err != nil {
				return err
			}
			start := time.Now()
			res, err := pg.ImportArchive(f.Id, *f.RatingType, *f.Period, r)
			_ = r.Close()
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
