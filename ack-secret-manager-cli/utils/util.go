package utils

import "regexp"

func IsMatchRegex(regex, str string) bool {
	reg := regexp.MustCompile(regex)
	return reg.MatchString(str)
}
