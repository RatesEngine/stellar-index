package timescale

import (
	"context"
	"strings"
	"testing"
)

// Defensive-guard coverage. The full INSERT/SELECT round-trip lives in
// test/integration/ per the established testcontainers-go pattern.

func TestUpsertProtocolContract_rejectsEmptySource(t *testing.T) {
	s := &Store{}
	err := s.UpsertProtocolContract(context.Background(), "", "Cchild", "Cfactory", 1)
	if err == nil || !strings.Contains(err.Error(), "source or contract_id") {
		t.Errorf("err=%v should mention empty source or contract_id", err)
	}
}

func TestUpsertProtocolContract_rejectsEmptyContract(t *testing.T) {
	s := &Store{}
	err := s.UpsertProtocolContract(context.Background(), "blend", "", "Cfactory", 1)
	if err == nil || !strings.Contains(err.Error(), "source or contract_id") {
		t.Errorf("err=%v should mention empty source or contract_id", err)
	}
}

func TestUpsertProtocolContract_rejectsEmptyFactory(t *testing.T) {
	s := &Store{}
	err := s.UpsertProtocolContract(context.Background(), "blend", "Cchild", "", 1)
	if err == nil || !strings.Contains(err.Error(), "factory_id") {
		t.Errorf("err=%v should mention empty factory_id", err)
	}
}

func TestLoadProtocolContracts_rejectsEmptySource(t *testing.T) {
	s := &Store{}
	_, err := s.LoadProtocolContracts(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "empty source") {
		t.Errorf("err=%v should mention empty source", err)
	}
}
