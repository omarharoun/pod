package embed

import "strings"

// stem strips the handful of English suffixes that separate two people writing the same
// goal — "run every morning" against "running every morning".
//
// Deliberately not a Porter stemmer. Porter is aggressive in ways that matter here:
// it conflates words this domain needs apart, and its output is not a word, so a lexicon
// entry cannot be read and checked by a person. This strips four suffixes conservatively
// and refuses to produce a stump shorter than three letters, which is the failure mode
// that makes unrelated goals look alike.
//
// It only has to be *consistent*: every surface form in the lexicon goes through it too,
// so a stem that is not a real word still lands on the right concept.
func stem(word string) string {
	switch {
	case strings.HasSuffix(word, "ies") && len(word) > 4:
		return word[:len(word)-3] + "y"
	case strings.HasSuffix(word, "sses"):
		return word[:len(word)-2]
	// "boxes" was becoming "boxe", so a goal saying "boxes" and one saying "box" landed on
	// different entries. These are the endings English pluralises with -es rather than -s.
	case len(word) > 4 && strings.HasSuffix(word, "es") &&
		(strings.HasSuffix(word, "ches") || strings.HasSuffix(word, "shes") ||
			strings.HasSuffix(word, "xes") || strings.HasSuffix(word, "zes") ||
			strings.HasSuffix(word, "oes")):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "ing") && len(word) > 5:
		return undouble(word[:len(word)-3])
	case strings.HasSuffix(word, "ed") && len(word) > 4:
		return undouble(word[:len(word)-2])
	case strings.HasSuffix(word, "ly") && len(word) > 5:
		return word[:len(word)-2]
	case strings.HasSuffix(word, "s") &&
		!strings.HasSuffix(word, "ss") &&
		!strings.HasSuffix(word, "us") &&
		!strings.HasSuffix(word, "is") &&
		len(word) > 3:
		return word[:len(word)-1]
	default:
		return word
	}
}

// undouble turns "runn" back into "run" and "jogg" into "jog", while leaving "call" and
// "spell" alone — those are real words whose doubled letter is not an inflection.
func undouble(word string) string {
	if len(word) < 4 {
		return word
	}
	last := word[len(word)-1]
	if last != word[len(word)-2] {
		return word
	}
	if strings.ContainsRune("aeiou", rune(last)) || last == 'l' || last == 's' || last == 'z' {
		return word
	}
	return word[:len(word)-1]
}
