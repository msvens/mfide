package fide

import (
	"archive/zip"
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/msvens/mfide/internal/dao"
)

// Player is one <player> element. The combined current list has every field;
// the monthly archive files have one rating type, in rating/games/k, and no
// rapid_* or blitz_* elements.
type Player struct {
	FideID      int    `xml:"fideid"`
	Name        string `xml:"name"`
	Country     string `xml:"country"`
	Sex         string `xml:"sex"`
	Title       string `xml:"title"`
	WTitle      string `xml:"w_title"`
	OTitle      string `xml:"o_title"`
	FoaTitle    string `xml:"foa_title"`
	Rating      int    `xml:"rating"`
	Games       int    `xml:"games"`
	K           int    `xml:"k"`
	RapidRating int    `xml:"rapid_rating"`
	RapidGames  int    `xml:"rapid_games"`
	RapidK      int    `xml:"rapid_k"`
	BlitzRating int    `xml:"blitz_rating"`
	BlitzGames  int    `xml:"blitz_games"`
	BlitzK      int    `xml:"blitz_k"`
	Birthday    string `xml:"birthday"` // birth year only; empty for ~34k players
	Flag        string `xml:"flag"`     // '', i (inactive), w (woman), wi
}

// Row converts a combined-list player to a player table row.
func (p *Player) Row() (*dao.Player, error) {
	r := &dao.Player{
		FideID: p.FideID, Name: p.Name, Country: p.Country, Sex: p.Sex,
		Title: p.Title, WTitle: p.WTitle, OTitle: p.OTitle, FoaTitle: p.FoaTitle, Flag: p.Flag,
		StdRating: p.Rating, StdGames: p.Games, StdK: p.K,
		RapidRating: p.RapidRating, RapidGames: p.RapidGames, RapidK: p.RapidK,
		BlitzRating: p.BlitzRating, BlitzGames: p.BlitzGames, BlitzK: p.BlitzK,
	}
	if b := strings.TrimSpace(p.Birthday); b != "" {
		y, err := strconv.Atoi(b)
		if err != nil {
			return nil, fmt.Errorf("player %d: birthday %q: %w", p.FideID, p.Birthday, err)
		}
		r.BirthYear = &y
	}
	return r, nil
}

// PlayerReader streams players from a FIDE zip without unzipping it to disk or
// holding more than one player in memory.
type PlayerReader struct {
	zr  *zip.ReadCloser
	rc  io.ReadCloser
	dec *xml.Decoder
}

// OpenZip opens the first .xml entry in a FIDE download.
func OpenZip(path string) (*PlayerReader, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			_ = zr.Close()
			return nil, err
		}
		return &PlayerReader{zr: zr, rc: rc, dec: xml.NewDecoder(bufio.NewReaderSize(rc, 1<<20))}, nil
	}
	_ = zr.Close()
	return nil, fmt.Errorf("%s: no .xml file in the zip", path)
}

// Next returns the next player, or io.EOF after the last one.
func (r *PlayerReader) Next() (*Player, error) {
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return nil, err // io.EOF at the end of the document
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "player" {
			p := &Player{}
			if err := r.dec.DecodeElement(p, &se); err != nil {
				return nil, err
			}
			return p, nil
		}
	}
}

// NextRow is Next converted to a player table row; it makes a PlayerReader a
// dao.PlayerSource.
func (r *PlayerReader) NextRow() (*dao.Player, error) {
	p, err := r.Next()
	if err != nil {
		return nil, err
	}
	return p.Row()
}

// NextArchiveRow is Next for a monthly per-type archive file; it makes a
// PlayerReader a dao.ArchiveSource.
func (r *PlayerReader) NextArchiveRow() (*dao.ArchiveRow, error) {
	p, err := r.Next()
	if err != nil {
		return nil, err
	}
	return &dao.ArchiveRow{FideID: p.FideID, Rating: p.Rating, Games: p.Games, K: p.K, Flag: p.Flag}, nil
}

func (r *PlayerReader) Close() error {
	return errors.Join(r.rc.Close(), r.zr.Close())
}
