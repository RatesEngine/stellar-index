package pipeline

import "os"

// awsResponseChecksumValidationEnv is the aws-sdk-go-v2 knob that
// decides whether the SDK tries to validate a checksum on every S3
// response payload.
const awsResponseChecksumValidationEnv = "AWS_RESPONSE_CHECKSUM_VALIDATION"

// QuietS3ChecksumWarnings defaults AWS_RESPONSE_CHECKSUM_VALIDATION to
// "when_required" unless the operator has set it explicitly.
//
// Since aws-sdk-go-v2/config v1.29.0 the SDK's default response-
// checksum mode is "when_supported": it attempts to validate a
// checksum on every S3 GET and, when the response carries none,
// logs at WARN level
//
//	Response has no supported checksum. Not validating response payload.
//
// galexie stores LedgerCloseMeta in MinIO, whose GetObject responses
// do not carry the CRC checksums the SDK now expects, so *every*
// ledger read emits that line. A verify-archive chain walk reads
// ~1200 ledgers/s — the warnings ballooned /tmp/va-full.log to
// 1.65 GB and buried the real verify-archive failure (#62) under
// noise journald then rate-dropped.
//
// "when_required" tells the SDK to validate only when the operation
// itself requires it — S3 GetObject does not — so the per-response
// attempt-and-warn stops. We default rather than force the value: an
// operator who sets the variable explicitly keeps their choice.
//
// Call this once at process start, before any S3 datastore is built;
// the SDK reads the variable when it loads its config.
func QuietS3ChecksumWarnings() {
	if _, set := os.LookupEnv(awsResponseChecksumValidationEnv); !set {
		_ = os.Setenv(awsResponseChecksumValidationEnv, "when_required")
	}
}
