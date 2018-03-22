package main

import (
	"regexp"
)

type matcher []*regexp.Regexp

func (m matcher) MatchAnyRule(request ModifiedRequest) bool {

	if request.Path == "" {
		return false
	}
	for _, matcher := range m {
		if matcher.MatchString(request.Path) {
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
