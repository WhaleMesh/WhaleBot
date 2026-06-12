package store

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var slugSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteByte('-')
		}
	}
	s := slugSanitizer.ReplaceAllString(b.String(), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "skill"
	}
	return s
}

func UniqueSlug(base string, exists func(string) bool) string {
	if !exists(base) {
		return base
	}
	for i := 2; i < 10_000; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if !exists(candidate) {
			return candidate
		}
	}
	return base + "-x"
}
