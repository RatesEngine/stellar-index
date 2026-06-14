package xdrjson_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/xdrjson"
)

func TestParticipantAccounts_Payment(t *testing.T) {
	const dest = "GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"
	body, _ := xdr.NewOperationBody(xdr.OperationTypePayment, xdr.PaymentOp{
		Destination: xdr.MustMuxedAddress(dest),
		Asset:       xdr.MustNewNativeAsset(),
		Amount:      1,
	})
	b64, _ := xdr.MarshalBase64(body)
	got, err := xdrjson.ParticipantAccounts(b64)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != dest {
		t.Errorf("participants = %v, want [%s]", got, dest)
	}
}

func TestParticipantAccounts_NoneForSelfContained(t *testing.T) {
	// manage_data has no counterparty account field → no participants.
	body, _ := xdr.NewOperationBody(xdr.OperationTypeManageData, xdr.ManageDataOp{DataName: "k"})
	b64, _ := xdr.MarshalBase64(body)
	got, _ := xdrjson.ParticipantAccounts(b64)
	if len(got) != 0 {
		t.Errorf("participants = %v, want none", got)
	}
}
