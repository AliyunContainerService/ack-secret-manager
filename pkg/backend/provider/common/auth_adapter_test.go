package common

// auth_adapter_test.go adds direct, table-driven coverage for OOSAuthAdapter.
// The KMS adapter is already exercised end-to-end through BuildAuthConfig in
// auth_helper_test.go, but the OOS adapter had no direct tests. These tests
// pin two things per getter: the populated pass-through value AND the nil-safe
// behavior when the embedded *OOSAuth is nil (every getter must fail closed
// rather than panic).

import (
	"testing"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// fullyPopulatedOOSAuth returns an OOSAuth with every field set to a distinct
// value so a getter that reads the wrong field is caught.
func fullyPopulatedOOSAuth() *v1alpha1.OOSAuth {
	return &v1alpha1.OOSAuth{
		AccessKey:                &v1alpha1.SecretRef{Name: "ak-secret", Key: "ak"},
		AccessKeySecret:          &v1alpha1.SecretRef{Name: "sk-secret", Key: "sk"},
		RAMRoleARN:               "acs:ram::123456789:role/oos-role",
		RAMRoleSessionName:       "oos-session",
		OIDCProviderARN:          "acs:ram::123456789:oidc-provider/oos-oidc",
		OIDCTokenFilePath:        "/var/run/secrets/token",
		RoleSessionExpiration:    "3600",
		RemoteRAMRoleARN:         "acs:ram::987654321:role/oos-remote",
		RemoteRAMRoleSessionName: "oos-remote-session",
		ServiceAccountRef:        &v1alpha1.ServiceAccountRef{Name: "oos-sa"},
	}
}

// TestOOSAuthAdapterStringGetters covers every string-returning getter for a
// populated adapter and for the nil-OOSAuth fail-closed case in one table.
func TestOOSAuthAdapterStringGetters(t *testing.T) {
	populated := &OOSAuthAdapter{OOSAuth: fullyPopulatedOOSAuth(), StoreName: "oos-store"}
	nilAuth := &OOSAuthAdapter{OOSAuth: nil, StoreName: "oos-store"}

	cases := []struct {
		name        string
		get         func(a *OOSAuthAdapter) string
		wantPopulat string
		wantNil     string
	}{
		{"RAMRoleARN", (*OOSAuthAdapter).GetRAMRoleARN, "acs:ram::123456789:role/oos-role", ""},
		{"RAMRoleSessionName", (*OOSAuthAdapter).GetRAMRoleSessionName, "oos-session", ""},
		{"OIDCProviderARN", (*OOSAuthAdapter).GetOIDCProviderARN, "acs:ram::123456789:oidc-provider/oos-oidc", ""},
		{"OIDCTokenFilePath", (*OOSAuthAdapter).GetOIDCTokenFilePath, "/var/run/secrets/token", ""},
		{"RoleSessionExpiration", (*OOSAuthAdapter).GetRoleSessionExpiration, "3600", ""},
		{"RemoteRAMRoleARN", (*OOSAuthAdapter).GetRemoteRAMRoleARN, "acs:ram::987654321:role/oos-remote", ""},
		{"RemoteRAMRoleSessionName", (*OOSAuthAdapter).GetRemoteRAMRoleSessionName, "oos-remote-session", ""},
		// GetSecretStoreName reads StoreName, which is independent of OOSAuth,
		// so it returns the store name in both cases.
		{"SecretStoreName", (*OOSAuthAdapter).GetSecretStoreName, "oos-store", "oos-store"},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/populated", func(t *testing.T) {
			if got := tc.get(populated); got != tc.wantPopulat {
				t.Errorf("%s() = %q, want %q", tc.name, got, tc.wantPopulat)
			}
		})
		t.Run(tc.name+"/nilAuth", func(t *testing.T) {
			if got := tc.get(nilAuth); got != tc.wantNil {
				t.Errorf("%s() with nil OOSAuth = %q, want %q", tc.name, got, tc.wantNil)
			}
		})
	}
}

// TestOOSAuthAdapterRefGetters covers the pointer-returning getters: the
// populated adapter returns the exact referenced object, and the nil-OOSAuth
// case returns nil without panicking.
func TestOOSAuthAdapterRefGetters(t *testing.T) {
	auth := fullyPopulatedOOSAuth()
	populated := &OOSAuthAdapter{OOSAuth: auth, StoreName: "oos-store"}
	nilAuth := &OOSAuthAdapter{OOSAuth: nil, StoreName: "oos-store"}

	t.Run("GetServiceAccountRef", func(t *testing.T) {
		if got := populated.GetServiceAccountRef(); got != auth.ServiceAccountRef {
			t.Errorf("GetServiceAccountRef() = %v, want the referenced ServiceAccountRef", got)
		}
		if got := nilAuth.GetServiceAccountRef(); got != nil {
			t.Errorf("GetServiceAccountRef() with nil OOSAuth = %v, want nil", got)
		}
	})

	t.Run("GetAccessKey", func(t *testing.T) {
		if got := populated.GetAccessKey(); got != auth.AccessKey {
			t.Errorf("GetAccessKey() = %v, want the referenced AccessKey SecretRef", got)
		}
		if got := nilAuth.GetAccessKey(); got != nil {
			t.Errorf("GetAccessKey() with nil OOSAuth = %v, want nil", got)
		}
	})

	t.Run("GetAccessKeySecret", func(t *testing.T) {
		if got := populated.GetAccessKeySecret(); got != auth.AccessKeySecret {
			t.Errorf("GetAccessKeySecret() = %v, want the referenced AccessKeySecret SecretRef", got)
		}
		if got := nilAuth.GetAccessKeySecret(); got != nil {
			t.Errorf("GetAccessKeySecret() with nil OOSAuth = %v, want nil", got)
		}
	})
}
