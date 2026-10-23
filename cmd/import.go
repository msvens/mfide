package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		fmt.Printf("changes among existing players: profile %d, rating %d, flag %d, returned %d\n",
			res.Changes["profile"], res.Changes["rating"], res.Changes["flag"], res.Changes["returned"])
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
	importCmd.AddCommand(importCurrentCmd, importRunsCmd)
	rootCmd.AddCommand(importCmd)
}
