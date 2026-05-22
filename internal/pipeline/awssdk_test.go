package pipeline

import (
	"os"
	"testing"
)

func TestQuietS3ChecksumWarnings(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		// t.Setenv registers a cleanup that restores the original
		// value; unset it afterwards so the function under test sees
		// a genuinely-absent variable.
		t.Setenv(awsResponseChecksumValidationEnv, "")
		if err := os.Unsetenv(awsResponseChecksumValidationEnv); err != nil {
			t.Fatalf("unset: %v", err)
		}

		QuietS3ChecksumWarnings()

		if got := os.Getenv(awsResponseChecksumValidationEnv); got != "when_required" {
			t.Fatalf("got %q, want %q", got, "when_required")
		}
	})

	t.Run("respects an explicit operator value", func(t *testing.T) {
		t.Setenv(awsResponseChecksumValidationEnv, "when_supported")

		QuietS3ChecksumWarnings()

		if got := os.Getenv(awsResponseChecksumValidationEnv); got != "when_supported" {
			t.Fatalf("overrode operator value: got %q, want %q", got, "when_supported")
		}
	})
}
