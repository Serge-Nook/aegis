package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func verbs(s string) string {
	v := verbRe.FindAllString(s, -1)
	sort.Strings(v)
	return strings.Join(v, " ")
}

func TestDictFormatVerbs(t *testing.T) {
	for ru, tr := range dict {
		for i, s := range tr {
			if s == "" {
				t.Errorf("%q: empty translation #%d", ru, i)
			}
			if verbs(s) != verbs(ru) {
				t.Errorf("%q: verbs mismatch in %q", ru, s)
			}
		}
	}
}
