// Package copy resolves user-facing strings from the catalogue generated out of
// content/microcopy.json.
//
// The server produces user-facing text in exactly two places: transactional email, and
// (from M4) push notification bodies. Both go through here. Everything else the server
// returns is a copy *key* for the client to resolve — see ADR-003.
package copy

import (
	"fmt"
	"sort"
	"strings"
)

// Resolve returns the catalogue string for key with {placeholders} substituted.
//
// It is an error to leave a placeholder unfilled: a half-interpolated sentence is worse
// than no sentence, and this is text a person reads.
func Resolve(key Key, params map[string]string) (string, error) {
	return ResolvePlural(key, 1, params)
}

// ResolvePlural picks the CLDR form for count. English only has "one" and "other"; the
// signature takes the count rather than the form so that adding a locale doesn't change
// every call site.
func ResolvePlural(key Key, count int, params map[string]string) (string, error) {
	forms, ok := catalogue[key]
	if !ok {
		return "", fmt.Errorf("copy: no such key %q", key)
	}
	template, ok := forms[pluralCategory(count)]
	if !ok {
		template, ok = forms["other"]
	}
	if !ok {
		return "", fmt.Errorf("copy: key %q has no usable form", key)
	}
	return interpolate(key, template, params)
}

// Has reports whether the catalogue contains key. Useful for asserting in tests that a
// key referenced by the API actually resolves.
func Has(key Key) bool {
	_, ok := catalogue[key]
	return ok
}

// pluralCategory implements the en-US plural rules. A locale with richer rules gets its
// own implementation here, not at the call sites.
func pluralCategory(count int) string {
	if count == 1 {
		return "one"
	}
	return "other"
}

func interpolate(key Key, template string, params map[string]string) (string, error) {
	var missing []string
	var b strings.Builder
	for i := 0; i < len(template); i++ {
		if template[i] != '{' {
			b.WriteByte(template[i])
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			b.WriteString(template[i:])
			break
		}
		name := template[i+1 : i+end]
		value, ok := params[name]
		if !ok {
			missing = append(missing, name)
		}
		b.WriteString(value)
		i += end
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("copy: key %q needs placeholder(s) %s", key, strings.Join(missing, ", "))
	}
	return b.String(), nil
}
