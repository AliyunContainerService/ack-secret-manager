package utils

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	BinaryType = "binary"
)

var clusterIDPattern = regexp.MustCompile(`^c[0-9a-z]{32}$`)

func IsClusterNamespace(s string) bool {
	return clusterIDPattern.MatchString(s)
}

type ConditionFunc func() (bool, error)

// Retry retries f every interval until after maxRetries.
// The interval won't be affected by how long f takes.
// For example, if interval is 3s, f takes 1s, another f will be called 2s later.
// However, if f takes longer than interval, it will be delayed.
func Retry(interval time.Duration, maxRetries int, f ConditionFunc) error {
	if maxRetries <= 0 {
		return fmt.Errorf("maxRetries (%d) should be > 0", maxRetries)
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for i := 0; ; i++ {
		ok, err := f()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if i == maxRetries {
			break
		}
		<-tick.C
	}
	return errors.New(fmt.Sprintf("still failing after %d retries", maxRetries))
}

func Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func Remove(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}
