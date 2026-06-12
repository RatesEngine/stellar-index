package cctp

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// ErrUnknownEvent flags an event whose topic[0] symbol isn't one
// of CCTP's four. Per CLAUDE.md ("Comet uses a shared topic")
// the dispatcher matches by topic bytes; another protocol could
// in theory emit the same symbol. Consumer should drop by
// ContractID before invoking the decoder.
var ErrUnknownEvent = errors.New("cctp: unknown event topic")

// ErrMalformedBody surfaces a schema drift — body Map missing a
// field the contract source declares, or a field with the wrong
// SCVal kind. Per CLAUDE.md ("Soroban DeFi contracts upgrade in
// place"), a contract upgrade could change field shapes; this
// error is the canary.
var ErrMalformedBody = errors.New("cctp: malformed event body")

// ErrMalformedTopic flags a topic slice shorter than the event
// type requires.
var ErrMalformedTopic = errors.New("cctp: malformed event topics")

// Classify reports which CCTP event the given Event is, or empty
// string if topic[0] doesn't match. Contract-ID filtering happens
// DOWNSTREAM.
func Classify(e *events.Event) string {
	if len(e.Topic) < 1 {
		return ""
	}
	switch e.Topic[0] {
	case TopicSymbolDepositForBurn:
		return EventDepositForBurn
	case TopicSymbolMintAndWithdraw:
		return EventMintAndWithdraw
	case TopicSymbolMessageSent:
		return EventMessageSent
	case TopicSymbolMessageReceived:
		return EventMessageReceived
	}
	return ""
}

// DecodeDepositForBurn turns one DepositForBurn-shaped Event into
// a canonical DepositForBurn value.
//
// Topic layout:
//
//	topic[0] = Symbol("deposit_for_burn")
//	topic[1] = Address(burn_token)
//	topic[2] = Address(depositor)
//	topic[3] = U32(min_finality_threshold)
//
// Body (ScMap, alphabetical-by-key):
//
//	{ amount: i128, destination_caller: BytesN<32>,
//	  destination_domain: u32, destination_token_messenger: BytesN<32>,
//	  hook_data: Bytes, max_fee: i128, mint_recipient: BytesN<32> }
//
// Per ADR-0013 we use scval helpers; the inferred-type entries
// slice keeps xdr out of this file's import list.
func DecodeDepositForBurn(e *events.Event) (DepositForBurn, error) { //nolint:gocognit,funlen,gocyclo // straight-line field decode; splitting fans the per-field error context across helpers
	if len(e.Topic) < 4 {
		return DepositForBurn{}, fmt.Errorf("%w: deposit_for_burn needs 4 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}

	burnTokenSV, err := scval.Parse(e.Topic[1])
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn topic[1] parse: %w", err)
	}
	burnToken, err := scval.AsAddressStrkey(burnTokenSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn burn_token: %w", err)
	}

	depositorSV, err := scval.Parse(e.Topic[2])
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn topic[2] parse: %w", err)
	}
	depositor, err := scval.AsAddressStrkey(depositorSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn depositor: %w", err)
	}

	minFinSV, err := scval.Parse(e.Topic[3])
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn topic[3] parse: %w", err)
	}
	minFinalityThreshold, err := scval.AsU32(minFinSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn min_finality_threshold: %w", err)
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn body parse: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn body not a map: %w", err)
	}

	amountSV, err := scval.MustMapField(entries, "amount")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'amount': %w", ErrMalformedBody, err)
	}
	amount, err := scval.AsAmountFromI128(amountSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn amount: %w", err)
	}

	mintRecipientSV, err := scval.MustMapField(entries, "mint_recipient")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'mint_recipient': %w", ErrMalformedBody, err)
	}
	mintRecipientBytes, err := scval.AsBytes(mintRecipientSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn mint_recipient: %w", err)
	}

	destDomainSV, err := scval.MustMapField(entries, "destination_domain")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'destination_domain': %w", ErrMalformedBody, err)
	}
	destinationDomain, err := scval.AsU32(destDomainSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn destination_domain: %w", err)
	}

	destTokenMsgSV, err := scval.MustMapField(entries, "destination_token_messenger")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'destination_token_messenger': %w", ErrMalformedBody, err)
	}
	destinationTokenMessenger, err := scval.AsBytes(destTokenMsgSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn destination_token_messenger: %w", err)
	}

	destCallerSV, err := scval.MustMapField(entries, "destination_caller")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'destination_caller': %w", ErrMalformedBody, err)
	}
	destinationCaller, err := scval.AsBytes(destCallerSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn destination_caller: %w", err)
	}

	maxFeeSV, err := scval.MustMapField(entries, "max_fee")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'max_fee': %w", ErrMalformedBody, err)
	}
	maxFee, err := scval.AsAmountFromI128(maxFeeSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn max_fee: %w", err)
	}

	hookDataSV, err := scval.MustMapField(entries, "hook_data")
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("%w: missing 'hook_data': %w", ErrMalformedBody, err)
	}
	hookData, err := scval.AsBytes(hookDataSV)
	if err != nil {
		return DepositForBurn{}, fmt.Errorf("cctp: deposit_for_burn hook_data: %w", err)
	}

	return DepositForBurn{
		Ledger:                    e.Ledger,
		TxHash:                    e.TxHash,
		OpIndex:                   e.OperationIndex,
		ClosedAt:                  e.LedgerClosedAt,
		ContractID:                e.ContractID,
		BurnToken:                 burnToken,
		Depositor:                 depositor,
		MinFinalityThreshold:      minFinalityThreshold,
		Amount:                    amount.String(),
		MintRecipient:             hex.EncodeToString(mintRecipientBytes),
		DestinationDomain:         destinationDomain,
		DestinationTokenMessenger: hex.EncodeToString(destinationTokenMessenger),
		DestinationCaller:         hex.EncodeToString(destinationCaller),
		MaxFee:                    maxFee.String(),
		HookData:                  hex.EncodeToString(hookData),
	}, nil
}

// DecodeMintAndWithdraw turns one MintAndWithdraw event into the
// canonical struct.
//
// Topic layout:
//
//	topic[0] = Symbol("mint_and_withdraw")
//	topic[1] = Address(mint_recipient)
//	topic[2] = Address(mint_token)
//
// Body: { amount: i128, fee_collected: i128 }.
func DecodeMintAndWithdraw(e *events.Event) (MintAndWithdraw, error) {
	if len(e.Topic) < 3 {
		return MintAndWithdraw{}, fmt.Errorf("%w: mint_and_withdraw needs 3 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}

	mintRecipientSV, err := scval.Parse(e.Topic[1])
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw topic[1] parse: %w", err)
	}
	mintRecipient, err := scval.AsAddressStrkey(mintRecipientSV)
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw mint_recipient: %w", err)
	}

	mintTokenSV, err := scval.Parse(e.Topic[2])
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw topic[2] parse: %w", err)
	}
	mintToken, err := scval.AsAddressStrkey(mintTokenSV)
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw mint_token: %w", err)
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw body parse: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw body not a map: %w", err)
	}

	amountSV, err := scval.MustMapField(entries, "amount")
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("%w: missing 'amount': %w", ErrMalformedBody, err)
	}
	amount, err := scval.AsAmountFromI128(amountSV)
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw amount: %w", err)
	}

	feeSV, err := scval.MustMapField(entries, "fee_collected")
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("%w: missing 'fee_collected': %w", ErrMalformedBody, err)
	}
	feeCollected, err := scval.AsAmountFromI128(feeSV)
	if err != nil {
		return MintAndWithdraw{}, fmt.Errorf("cctp: mint_and_withdraw fee_collected: %w", err)
	}

	return MintAndWithdraw{
		Ledger:        e.Ledger,
		TxHash:        e.TxHash,
		OpIndex:       e.OperationIndex,
		ClosedAt:      e.LedgerClosedAt,
		ContractID:    e.ContractID,
		MintRecipient: mintRecipient,
		MintToken:     mintToken,
		Amount:        amount.String(),
		FeeCollected:  feeCollected.String(),
	}, nil
}

// DecodeMessageSent turns one MessageSent event into the canonical
// struct. Topic-only event (one topic, body carries the message).
//
// Wire shape:
//
//	topic[0] = Symbol("message_sent")
//	value    = ScMap { message: Bytes }   (or raw ScBytes if the
//	                                       macro layout shifts)
func DecodeMessageSent(e *events.Event) (MessageSent, error) {
	body, err := scval.Parse(e.Value)
	if err != nil {
		return MessageSent{}, fmt.Errorf("cctp: message_sent body parse: %w", err)
	}
	// Map-body path: #[contractevent] wraps the single field in a
	// named-Map. Fall through to raw Bytes if the macro shifts.
	if entries, mapErr := scval.AsMap(body); mapErr == nil {
		msgSV, err := scval.MustMapField(entries, "message")
		if err != nil {
			return MessageSent{}, fmt.Errorf("%w: missing 'message': %w", ErrMalformedBody, err)
		}
		msg, err := scval.AsBytes(msgSV)
		if err != nil {
			return MessageSent{}, fmt.Errorf("cctp: message_sent message: %w", err)
		}
		return MessageSent{
			Ledger:     e.Ledger,
			TxHash:     e.TxHash,
			OpIndex:    e.OperationIndex,
			ClosedAt:   e.LedgerClosedAt,
			ContractID: e.ContractID,
			Message:    hex.EncodeToString(msg),
		}, nil
	}
	// Raw-Bytes fallback.
	raw, err := scval.AsBytes(body)
	if err != nil {
		return MessageSent{}, fmt.Errorf("cctp: message_sent body neither map nor bytes: %w", err)
	}
	return MessageSent{
		Ledger:     e.Ledger,
		TxHash:     e.TxHash,
		OpIndex:    e.OperationIndex,
		ClosedAt:   e.LedgerClosedAt,
		ContractID: e.ContractID,
		Message:    hex.EncodeToString(raw),
	}, nil
}

// DecodeMessageReceived turns one MessageReceived event into the
// canonical struct.
//
// Topic layout:
//
//	topic[0] = Symbol("message_received")
//	topic[1] = Address(caller)
//	topic[2] = BytesN<32>(nonce)
//	topic[3] = U32(finality_threshold_executed)
//
// Body: { source_domain: u32, sender: BytesN<32>, message_body: Bytes }.
func DecodeMessageReceived(e *events.Event) (MessageReceived, error) {
	if len(e.Topic) < 4 {
		return MessageReceived{}, fmt.Errorf("%w: message_received needs 4 topics, got %d", ErrMalformedTopic, len(e.Topic))
	}

	callerSV, err := scval.Parse(e.Topic[1])
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received topic[1] parse: %w", err)
	}
	caller, err := scval.AsAddressStrkey(callerSV)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received caller: %w", err)
	}

	nonceSV, err := scval.Parse(e.Topic[2])
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received topic[2] parse: %w", err)
	}
	nonceBytes, err := scval.AsBytes(nonceSV)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received nonce: %w", err)
	}

	finExecSV, err := scval.Parse(e.Topic[3])
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received topic[3] parse: %w", err)
	}
	finalityExecuted, err := scval.AsU32(finExecSV)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received finality_threshold_executed: %w", err)
	}

	body, err := scval.Parse(e.Value)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received body parse: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received body not a map: %w", err)
	}

	sourceDomainSV, err := scval.MustMapField(entries, "source_domain")
	if err != nil {
		return MessageReceived{}, fmt.Errorf("%w: missing 'source_domain': %w", ErrMalformedBody, err)
	}
	sourceDomain, err := scval.AsU32(sourceDomainSV)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received source_domain: %w", err)
	}

	senderSV, err := scval.MustMapField(entries, "sender")
	if err != nil {
		return MessageReceived{}, fmt.Errorf("%w: missing 'sender': %w", ErrMalformedBody, err)
	}
	senderBytes, err := scval.AsBytes(senderSV)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received sender: %w", err)
	}

	messageBodySV, err := scval.MustMapField(entries, "message_body")
	if err != nil {
		return MessageReceived{}, fmt.Errorf("%w: missing 'message_body': %w", ErrMalformedBody, err)
	}
	messageBodyBytes, err := scval.AsBytes(messageBodySV)
	if err != nil {
		return MessageReceived{}, fmt.Errorf("cctp: message_received message_body: %w", err)
	}

	return MessageReceived{
		Ledger:                    e.Ledger,
		TxHash:                    e.TxHash,
		OpIndex:                   e.OperationIndex,
		ClosedAt:                  e.LedgerClosedAt,
		ContractID:                e.ContractID,
		Caller:                    caller,
		Nonce:                     hex.EncodeToString(nonceBytes),
		FinalityThresholdExecuted: finalityExecuted,
		SourceDomain:              sourceDomain,
		Sender:                    hex.EncodeToString(senderBytes),
		MessageBody:               hex.EncodeToString(messageBodyBytes),
	}, nil
}
