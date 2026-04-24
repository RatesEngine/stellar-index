package stellarrpc

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// InvokeContractTxEnvelope builds a base64-encoded TransactionEnvelope
// suitable for passing to [Client.SimulateTransaction]. No signing is
// performed — simulation ignores signatures — so the source account
// can be any well-formed G-strkey.
//
// Use cases:
//
//   - Soroswap factory all_pairs()/all_pairs_length() probes at boot
//     (internal/sources/soroswap bootstrap) so the decoder knows
//     every pair that exists before our first ledger.
//   - Any future "read current contract state" diagnostic.
//
// The envelope's Fee/SeqNum are set to placeholder values (100 and 0);
// stellar-rpc's simulate endpoint ignores them. Real tx submission
// would need genuine values + signatures.
//
// contractID is a C-strkey; functionName is the Symbol the caller
// targets; args are pre-built SCVals (typically empty for no-arg
// view functions like all_pairs_length).
func InvokeContractTxEnvelope(sourceAccount, contractID, functionName string, args []xdr.ScVal) (string, error) {
	if sourceAccount == "" {
		// Any valid G-strkey works — simulation ignores the source.
		// Use a documented "null" account: 32 zero bytes encoded as
		// a G-strkey. stellar-rpc accepts any well-formed strkey.
		var zero [32]byte
		enc, err := strkey.Encode(strkey.VersionByteAccountID, zero[:])
		if err != nil {
			return "", fmt.Errorf("stellarrpc: encode null source: %w", err)
		}
		sourceAccount = enc
	}

	muxed, err := parseSource(sourceAccount)
	if err != nil {
		return "", fmt.Errorf("stellarrpc: source %q: %w", sourceAccount, err)
	}

	ca, err := parseContractAddress(contractID)
	if err != nil {
		return "", fmt.Errorf("stellarrpc: contract %q: %w", contractID, err)
	}

	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
					InvokeContract: &xdr.InvokeContractArgs{
						ContractAddress: ca,
						FunctionName:    xdr.ScSymbol(functionName),
						Args:            xdr.ScVec(args),
					},
				},
			},
		},
	}

	tx := xdr.Transaction{
		SourceAccount: muxed,
		Fee:           100, // placeholder; ignored by simulate
		SeqNum:        0,   // placeholder; ignored by simulate
		Operations:    []xdr.Operation{op},
	}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1:   &xdr.TransactionV1Envelope{Tx: tx}, // no signatures
	}

	b64, err := xdr.MarshalBase64(env)
	if err != nil {
		return "", fmt.Errorf("stellarrpc: marshal envelope: %w", err)
	}
	return b64, nil
}

// parseSource turns a G-strkey into the xdr.MuxedAccount shape
// TransactionEnvelope.Tx.SourceAccount expects.
func parseSource(strkeyAddr string) (xdr.MuxedAccount, error) {
	raw, err := strkey.Decode(strkey.VersionByteAccountID, strkeyAddr)
	if err != nil {
		return xdr.MuxedAccount{}, err
	}
	var ed xdr.Uint256
	copy(ed[:], raw)
	return xdr.MuxedAccount{
		Type:    xdr.CryptoKeyTypeKeyTypeEd25519,
		Ed25519: &ed,
	}, nil
}

// parseContractAddress turns a C-strkey into the xdr.ScAddress
// shape InvokeContractArgs.ContractAddress expects.
func parseContractAddress(strkeyAddr string) (xdr.ScAddress, error) {
	raw, err := strkey.Decode(strkey.VersionByteContract, strkeyAddr)
	if err != nil {
		return xdr.ScAddress{}, err
	}
	var cid xdr.ContractId
	copy(cid[:], raw)
	return xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}, nil
}
