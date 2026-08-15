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

// validateKmsEndpoint prevents SSRF (CWE-918): the endpoint comes from
// user-controlled ExternalSecret CRs and is passed to the KMS SDK as-is, so
// it must be a bare lowercase hostname matching one of the three allowed
// patterns (no scheme/port/uppercase, which break DNS or signature
// computation). Callers guarantee a non-empty endpoint.
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
