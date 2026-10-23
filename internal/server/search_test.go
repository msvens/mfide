package server

import (
	"reflect"
	"strings"
	"testing"
)

func TestSearchTerms(t *testing.T) {
	cases := []struct {
		q    string
		want [][]string
	}{
		{"Olle Svensson", [][]string{{"olle"}, {"svensson"}}},
		{"  svensson,   olle ", [][]string{{"svensson"}, {"olle"}}},
		{"Boman. Simon", [][]string{{"boman"}, {"simon"}}},
		{"Stål", [][]string{{"stal"}}},
		{"Müller", [][]string{{"muller", "mueller"}}},
		{"Söderström", [][]string{{"soderstrom", "soderstroem", "soederstrom", "soederstroem"}}},
		{"Strømme", [][]string{{"stromme", "stroemme"}}},
		{"Angles d'Auriac", [][]string{{"angles"}, {"d"}, {"auriac"}}},
		{"Niccolò", [][]string{{"niccolo"}}},
		{"Straße", [][]string{{"strasse"}}},
		{"Ærø", [][]string{{"aero", "aeroe"}}},
		{"carlsen m", [][]string{{"carlsen"}, {"m"}}},
		{"Anderssonm Bjorn 1958", [][]string{{"anderssonm"}, {"bjorn"}, {"1958"}}},
	}
	for _, c := range cases {
		got, err := searchTerms(c.q)
		if err != nil {
			t.Errorf("searchTerms(%q): %v", c.q, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("searchTerms(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestSearchTermsCapsSpellings(t *testing.T) {
	got, err := searchTerms("öäüö")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0]) != maxAlts {
		t.Errorf("got %v, want one word with %d spellings", got, maxAlts)
	}
}

func TestSearchTermsRejects(t *testing.T) {
	for _, q := range []string{"", "   ", ",.-", "a b", "Иванов", strings.Repeat("x", maxQueryLen+1), "a1 b2 c3 d4 e5 f6"} {
		if got, err := searchTerms(q); err == nil {
			t.Errorf("searchTerms(%q) = %v, want an error", q, got)
		}
	}
}
