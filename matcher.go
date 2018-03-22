package main

import (
	"regexp"
)

var paths []*regexp.Regexp

func MatchAnyRule(request ModifiedRequest) bool {

	if request.Path == "" {
		return false
	}
	for _, matcher := range paths {
		if matcher.MatchString(request.Path) {
			return true
		}
	}
	return false
}

func AddMatcherRules(rules []string) error {
	for _, p := range rules {
		compiled, err := regexp.Compile(p)
		if err != nil {
			return err
		}
		paths = append(paths, compiled)
	}
	return nil
}
