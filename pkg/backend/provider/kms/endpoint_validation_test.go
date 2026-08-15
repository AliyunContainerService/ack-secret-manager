package kms

import (
	"testing"
)

func TestValidateKmsEndpoint(t *testing.T) {
	cases := []struct {
		desc     string
		endpoint string
		wantErr  bool
	}{
		// Valid endpoints
		{"VPC shared gateway", "kms-vpc.cn-hangzhou.aliyuncs.com", false},
		{"public shared gateway", "kms.cn-hangzhou.aliyuncs.com", false},
		{"dedicated gateway", "kst-hbb3xxx.cryptoservice.kms.aliyuncs.com", false},
		{"us-west-1", "kms-vpc.us-west-1.aliyuncs.com", false},
		{"ap-southeast-1", "kms-vpc.ap-southeast-1.aliyuncs.com", false},
		{"dedicated gateway finance region", "kst-1234abcd.cryptoservice.kms.aliyuncs.com", false},

		// Invalid: SDK requires bare lowercase hostname
		{"port causes signature mismatch", "kms-vpc.cn-hangzhou.aliyuncs.com:443", true},
		{"public gateway with port", "kms.cn-hangzhou.aliyuncs.com:443", true},
		{"scheme causes double-prefix DNS error", "https://kms-vpc.cn-hangzhou.aliyuncs.com", true},
		{"https with public gateway and port", "https://kms.cn-hangzhou.aliyuncs.com:443", true},
		{"uppercase causes signature mismatch", "KMS-VPC.cn-hangzhou.aliyuncs.com", true},
		{"mixed case rejected", "Kms.Cn-Hangzhou.Aliyuncs.com", true},
		{"http scheme rejected", "http://kms-vpc.cn-hangzhou.aliyuncs.com", true},
		{"attacker domain with http", "http://evil.attacker.com", true},

		// Invalid: SSRF attacks
		{"attacker domain", "evil.attacker.com", true},
		{"attacker domain with path", "evil.attacker.com/aliyuncs.com", true},
		{"non-KMS aliyuncs subdomain", "evil.aliyuncs.com", true},
		{"ECS endpoint (not KMS)", "ecs.cn-hangzhou.aliyuncs.com", true},
		{"RDS endpoint (not KMS)", "rds.cn-hangzhou.aliyuncs.com", true},
		{"OSS endpoint (not KMS)", "oss-cn-hangzhou.aliyuncs.com", true},
		{"attacker with kms prefix wrong domain", "kms-vpc.evil.com", true},
		{"attacker with cryptoservice wrong domain", "kst-xxx.cryptoservice.kms.evil.com", true},

		// Invalid: IP literals
		{"AWS metadata IP", "169.254.169.254", true},
		{"Alibaba metadata IP", "100.100.100.200", true},
		{"arbitrary IP", "10.0.0.1", true},
		{"IPv6 loopback", "::1", true},
		{"IPv6 link-local", "fd00:ec2::254", true},
		{"IP with port", "169.254.169.254:8080", true},

		// Invalid: metadata hostnames
		{"GCP metadata hostname", "metadata.google.internal", true},
		{"AWS metadata hostname", "metadata.aws.internal", true},

		// Invalid: other
		{"localhost", "localhost", true},
		{"internal service name", "kms-service.default.svc.cluster.local", true},
		// Invalid: empty or malformed
		{"empty string", "", true},
		{"whitespace only", "   ", true},
		{"https with empty host", "https://", true},
		{"just a port", ":443", true},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			err := validateKmsEndpoint(tc.endpoint)
			if tc.wantErr && err == nil {
				t.Errorf("validateKmsEndpoint(%q) expected error, got nil", tc.endpoint)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateKmsEndpoint(%q) expected no error, got: %v", tc.endpoint, err)
			}
		})
	}
}

func TestIsCryptoserviceEndpoint(t *testing.T) {
	cases := []struct {
		desc     string
		endpoint string
		want     bool
	}{
		{"dedicated gateway", "kst-xxx.cryptoservice.kms.aliyuncs.com", true},
		{"VPC gateway", "kms-vpc.cn-hangzhou.aliyuncs.com", false},
		{"public gateway", "kms.cn-hangzhou.aliyuncs.com", false},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := isCryptoserviceEndpoint(tc.endpoint)
			if got != tc.want {
				t.Errorf("isCryptoserviceEndpoint(%q) = %v, want %v", tc.endpoint, got, tc.want)
			}
		})
	}
}
