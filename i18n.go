package main

import "fmt"

// Languages in menu order; Russian is the source language of all strings.
var languages = []struct{ Code, Name string }{{"ru", "Русский"}, {"en", "English"}, {"zh", "中文"}}

var lang = "ru"

// T translates a Russian UI string into the current language.
func T(s string) string {
	if lang == "ru" {
		return s
	}
	if tr, ok := dict[s]; ok {
		if lang == "en" && tr[0] != "" {
			return tr[0]
		}
		if lang == "zh" && tr[1] != "" {
			return tr[1]
		}
	}
	return s
}

func Tf(s string, a ...any) string { return fmt.Sprintf(T(s), a...) }
