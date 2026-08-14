package kms

import (
	"fmt"
	"regexp"
	"strings"
)

// kmsEndpointRe matches exactly the three valid Alibaba Cloud KMS endpoint
// formats (all lowercase, bare hostname, no scheme/port/path):
//   - kms.<region>.aliyuncs.com                (public shared gateway)
//   - kms-vpc.<region>.aliyuncs.com            (VPC shared gateway)
//   - <instance-id>.cryptoservice.kms.aliyuncs.com (dedicated KMS gateway)
//
// DNS labels ([a-z0-9-]+) reject uppercase, IPs, and metadata hostnames
// inherently, so no additional checks are needed.
var kmsEndpointRe = regexp.MustCompile(
	`^(kms(-vpc)?\.[a-z0-9-]+\.aliyuncs\.com|[a-z0-9-]+\.cryptoservice\.kms\.aliyuncs\.com)$`,
)

// validateKmsEndpoint validates that a user-supplied KMS endpoint is a
// legitimate Alibaba Cloud KMS domain. It prevents SSRF attacks (CWE-918)
// by strictly matching the three documented endpoint patterns.
//
// The KmsEndpoint field comes from ExternalSecret CR YAML which is
// user-controlled. Without validation, a multi-tenant cluster user can
// redirect signed KMS API requests (carrying the controller's STS
// credentials) to an attacker-controlled host.
//
// The endpoint is passed to the Alibaba Cloud KMS SDK as-is, so it must
// be a bare lowercase hostname. The SDK prepends "https://" itself and
// uses the raw value in signature computation, therefore scheme prefixes,
// port suffixes, and uppercase characters all cause runtime failures:
//   - scheme ("https://...") → SDK produces "https://https//..." → DNS error
//   - port (":443")          → signature mismatch (IncompleteSignature)
//   - uppercase ("KMS.")     → signature mismatch (IncompleteSignature)
//
// The caller guarantees a non-empty endpoint: custom endpoints come from
// ExternalSecret CR fields, and the default endpoint comes from
// Provider.GetEndpoint() which always returns a valid value.
func validateKmsEndpoint(endpoint string) error {
	host := strings.TrimSpace(endpoint)
	if !kmsEndpointRe.MatchString(host) {
		return fmt.Errorf("KMS endpoint %q does not match any allowed KMS endpoint pattern "+
			"(expected kms.<region>.aliyuncs.com, kms-vpc.<region>.aliyuncs.com, "+
			"or <instance-id>.cryptoservice.kms.aliyuncs.com)", endpoint)
	}

	return nil
}

// isCryptoserviceEndpoint checks whether the endpoint is a dedicated KMS
// gateway (cryptoservice). It is always called after validateKmsEndpoint,
// so the endpoint is guaranteed to be a validated bare lowercase hostname.
func isCryptoserviceEndpoint(endpoint string) bool {
	return strings.HasSuffix(strings.TrimSpace(endpoint), ".cryptoservice.kms.aliyuncs.com")
}
