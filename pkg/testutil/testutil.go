// Package testutil provides small shared test-support helpers used across the
// project's package tests. It intentionally lives in a regular (importable)
// package because Go test helpers defined in *_test.go files cannot cross
// package boundaries.
package testutil

import (
	"bytes"
	"flag"
	"os"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// NewTestScheme builds a runtime scheme registered with the core v1 types and
// the project's alibabacloud API types, matching what the controller tests
// need for the controller-runtime fake client.
func NewTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add alibabacloud api to scheme: %v", err)
	}
	return scheme
}

// initKlogFlagsOnce guards the one-time klog flag registration: klog v1 only
// exposes its flags via an explicit InitFlags call.
var initKlogFlagsOnce sync.Once

// CaptureKlogOutput redirects klog output to a buffer for the duration of fn
// and returns everything it wrote. logtostderr must be flipped off for
// SetOutput to take effect.
func CaptureKlogOutput(t *testing.T, fn func()) string {
	t.Helper()
	initKlogFlagsOnce.Do(func() { klog.InitFlags(nil) })
	if err := flag.Set("logtostderr", "false"); err != nil {
		t.Fatalf("failed to disable logtostderr: %v", err)
	}
	defer func() {
		klog.SetOutput(os.Stderr)
		_ = flag.Set("logtostderr", "true")
	}()
	var buf bytes.Buffer
	klog.SetOutput(&buf)
	fn()
	klog.Flush()
	return buf.String()
}
