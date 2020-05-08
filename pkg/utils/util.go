/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"errors"
	"fmt"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
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

// getKubernetesClients returns all the required clients(token CRD client and origin k8s cli) to communicate with
func GetKubernetesClients() (dynamic.Interface, error) {
	var err error
	var cfg *rest.Config

	cfg, err = rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("error loading kubernetes configuration inside cluster, "+
			"check app is running outside kubernetes cluster or run in development mode: %s", err)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}
