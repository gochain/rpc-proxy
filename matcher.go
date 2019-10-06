package main

import (
	"regexp"
)

type matcher []*regexp.Regexp

func (m matcher) MatchAnyRule(method string) bool {
	if method == "" {
		return false
	}
	for _, matcher := range m {
		if matcher.MatchString(method) {
			return true
		}
	}
	return false
}

func newMatcher(rules []string) (matcher, error) {
	var m matcher
	for _, p := range rules {
		compiled, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		m = append(m, compiled)
	}
	return m, nil
}
