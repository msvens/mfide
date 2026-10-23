package server

import (
	"strings"
	"unicode"
)

// FIDE stores every name in ASCII, so a query is folded to match. Most accents
// just drop (Swedish names: "Stål" is "Stal", "Söderberg" is "Soderberg"), but
// German names usually spell umlauts out ("Müller" is mostly "Mueller"), so
// ä/ö/ü/ø match both forms.
var (
	foldTable = map[rune]string{
		'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'å': "a", 'ā': "a", 'ą': "a", 'ă': "a",
		'æ': "ae", 'ç': "c", 'ć': "c", 'č': "c", 'ď': "d", 'đ': "d", 'ð': "d",
		'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ę': "e", 'ě': "e",
		'ğ': "g", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ı': "i",
		'ł': "l", 'ľ': "l", 'ĺ': "l", 'ñ': "n", 'ń': "n", 'ň': "n",
		'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ő': "o", 'ō': "o", 'œ': "oe",
		'ř': "r", 'ŕ': "r", 'ś': "s", 'š': "s", 'ş': "s", 'ș': "s", 'ß': "ss",
		'ť': "t", 'ţ': "t", 'ț': "t", 'þ': "th",
		'ù': "u", 'ú': "u", 'û': "u", 'ů': "u", 'ű': "u", 'ū': "u",
		'ý': "y", 'ÿ': "y", 'ź': "z", 'ż': "z", 'ž': "z",
	}
	twoWays = map[rune][2]string{'ä': {"a", "ae"}, 'ö': {"o", "oe"}, 'ü': {"u", "ue"}, 'ø': {"o", "oe"}}
)

const (
	maxQueryLen   = 100
	maxSearchWord = 5
	maxAlts       = 4 // spellings per word; beyond this, umlauts fold one way only
)

// searchTerms turns a query into words, each with its possible ASCII spellings:
// "Müller, Jan" -> [[muller mueller] [jan]]. Characters that can't be folded to
// ASCII letters or digits separate words.
func searchTerms(q string) ([][]string, error) {
	if len([]rune(q)) > maxQueryLen {
		return nil, BadRequestError("query is too long")
	}
	var terms [][]string
	alts := []string{""}
	flush := func() {
		if alts[0] != "" {
			terms = append(terms, alts)
		}
		alts = []string{""}
	}
	add := func(s string) {
		for i := range alts {
			alts[i] += s
		}
	}
	for _, r := range strings.ToLower(q) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			add(string(r))
		case twoWays[r] != [2]string{}:
			w := twoWays[r]
			if len(alts)*2 > maxAlts {
				add(w[0])
				continue
			}
			next := make([]string, 0, len(alts)*2)
			for _, a := range alts {
				next = append(next, a+w[0], a+w[1])
			}
			alts = next
		case foldTable[r] != "":
			add(foldTable[r])
		default:
			flush()
		}
	}
	flush()

	if len(terms) == 0 {
		return nil, BadRequestError("query has no searchable words")
	}
	if len(terms) > maxSearchWord {
		return nil, BadRequestError("query has too many words")
	}
	for _, t := range terms {
		if len(t[0]) >= 2 {
			return terms, nil
		}
	}
	return nil, BadRequestError("query needs at least one word of two or more letters")
}
