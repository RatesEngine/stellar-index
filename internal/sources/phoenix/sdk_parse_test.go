package phoenix

import "testing"

// Parse errors are the third branch on each sdk decoder — wrong-kind
// (already pinned) and happy path are tested elsewhere; this file
// covers the invalid-base64 path so a malformed event body can't
// silently propagate as a zero asset/amount/strkey downstream.

func TestSdkDecodeAddress_invalidBase64(t *testing.T) {
	if _, err := sdkDecodeAddress("!!not-base64!!"); err == nil {
		t.Error("expected parse error for invalid base64, got nil")
	}
}

func TestSdkDecodeAsset_invalidBase64(t *testing.T) {
	if _, err := sdkDecodeAsset("@@bad@@"); err == nil {
		t.Error("expected parse error for invalid base64, got nil")
	}
}

func TestSdkDecodeI128_invalidBase64(t *testing.T) {
	if _, err := sdkDecodeI128("###garbage###"); err == nil {
		t.Error("expected parse error for invalid base64, got nil")
	}
}
