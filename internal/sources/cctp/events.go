// Package cctp decodes Circle's CCTP v2 contract events on
// Stellar (Soroban).
//
// Three on-chain contracts:
//
//	TokenMessengerMinter  CAE2G5Z77UP7GYPYGFOWFGW7C7J6I4YP2AFGSADRKQY62SYUFLPNFTXL
//	MessageTransmitter    CACMENFFJPJMSDAJQLX4R7K3SFZIW2LJSE3R2UMLGSWHFHS353FVXAZV
//	CctpForwarder         CBZL2IH7F6BIDAA3WBNXYKIXSATJGMSW7K5P5MJ6STX5RXN47TZJDF5T
//
// Four canonical events:
//
//	deposit_for_burn   (TokenMessengerMinter) — outbound transfer
//	mint_and_withdraw  (TokenMessengerMinter) — inbound mint
//	message_sent       (MessageTransmitter)   — wire envelope (outbound)
//	message_received   (MessageTransmitter)   — wire envelope (inbound)
//
// One outbound `deposit_for_burn` call emits BOTH a DepositForBurn
// event AND a MessageSent event in the same transaction —
// correlate by (ledger, tx_hash) when assembling a logical
// outbound-transfer record. Same for inbound (MessageReceived +
// MintAndWithdraw).
//
// Design rationale and full per-event schemas extracted from the
// contracts' Rust source: docs/architecture/cctp-stellar-coverage.md.
//
// Wiring (#40): decode.go decodes; consumer.go projects each event
// into the canonical cctp.Event row; dispatcher_adapter.go is the
// dispatcher Decoder; the indexer's sink persists via
// Store.InsertCCTPEvent into the cctp_events hypertable
// (migration 0038, per-protocol table — operator-confirmed
// 2026-05-22). See README.md §Wiring.
package cctp

import (
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName is the registry key for this source.
const SourceName = "cctp"

// Mainnet contract addresses — verified 2026-05-20 against
// https://developers.circle.com/cctp/references/stellar-contracts
// + the upstream source repo github.com/circlefin/stellar-cctp.
const (
	MainnetTokenMessengerMinter = "CAE2G5Z77UP7GYPYGFOWFGW7C7J6I4YP2AFGSADRKQY62SYUFLPNFTXL"
	MainnetMessageTransmitter   = "CACMENFFJPJMSDAJQLX4R7K3SFZIW2LJSE3R2UMLGSWHFHS353FVXAZV"
	MainnetCctpForwarder        = "CBZL2IH7F6BIDAA3WBNXYKIXSATJGMSW7K5P5MJ6STX5RXN47TZJDF5T"
)

// StellarDomainID is Stellar's CCTP domain identifier
// (`message_transmitter::get_local_domain()` returns this value).
// Other notable CCTP domains: Ethereum=0, Avalanche=1, Arbitrum=3,
// Solana=7. Full list at Circle docs.
const StellarDomainID uint32 = 27

// Event names — the symbol_short / Symbol::new strings emitted as
// topic[0] by each #[contractevent]. Verified against
// contracts/{token-messenger-minter-v2,message-transmitter-v2}/src/lib.rs.
const (
	EventDepositForBurn  = "deposit_for_burn"  // TokenMessengerMinter
	EventMintAndWithdraw = "mint_and_withdraw" // TokenMessengerMinter
	EventMessageSent     = "message_sent"      // MessageTransmitter
	EventMessageReceived = "message_received"  // MessageTransmitter
)

// Topic[0] pre-encoded base64 — package-init constants so
// Classify() does single string-equal comparisons rather than
// full SCVal decodes per event. All four are >= 12 chars (a
// `deposit_for_burn` is 16) so the Soroban macro emits them as
// long-form ScSymbol via `Symbol::new(env, …)`, not the
// 9-char-capped `symbol_short!`. The wire shape is still ScSymbol
// in both cases; the macro picks the constructor by length.
var (
	TopicSymbolDepositForBurn  = scval.MustEncodeSymbol(EventDepositForBurn)
	TopicSymbolMintAndWithdraw = scval.MustEncodeSymbol(EventMintAndWithdraw)
	TopicSymbolMessageSent     = scval.MustEncodeSymbol(EventMessageSent)
	TopicSymbolMessageReceived = scval.MustEncodeSymbol(EventMessageReceived)
)

// DepositForBurn is the canonical projection of one
// `DepositForBurn` event from TokenMessengerMinter (v2).
//
// Source schema (token-messenger-minter-v2/src/lib.rs:#[contractevent]):
//
//	pub struct DepositForBurn {
//	    #[topic] pub burn_token: Address,
//	    pub amount: i128,
//	    #[topic] pub depositor: Address,
//	    pub mint_recipient: BytesN<32>,
//	    pub destination_domain: u32,
//	    pub destination_token_messenger: BytesN<32>,
//	    pub destination_caller: BytesN<32>,
//	    pub max_fee: i128,
//	    #[topic] pub min_finality_threshold: u32,
//	    pub hook_data: Bytes,
//	}
//
// On the wire:
//
//	topics = ["deposit_for_burn", burn_token, depositor, min_finality_threshold]
//	body   = ScMap { amount, mint_recipient, destination_domain,
//	                 destination_token_messenger, destination_caller,
//	                 max_fee, hook_data }
//
// `mint_recipient` / `destination_token_messenger` /
// `destination_caller` are 32-byte buffers — for EVM destination
// chains the leading 12 bytes are zero padding and the trailing
// 20 bytes are the EVM address. We surface them as raw hex
// (lowercase, no 0x prefix) — downstream decides whether to
// re-format for a specific destination chain.
type DepositForBurn struct {
	Ledger     uint32
	TxHash     string
	OpIndex    int
	ClosedAt   string // RFC 3339
	ContractID string

	// Topics
	BurnToken            string // Stellar Address strkey
	Depositor            string // Stellar Address strkey
	MinFinalityThreshold uint32 // attestation finality requirement

	// Body
	Amount                    string // i128 canonical-decimals; see CCTP docs §canonical amounts
	MintRecipient             string // hex; BytesN<32>
	DestinationDomain         uint32 // CCTP domain ID (0=Ethereum, 1=Avalanche, ...)
	DestinationTokenMessenger string // hex; BytesN<32>
	DestinationCaller         string // hex; BytesN<32>; zero-hex = any-caller
	MaxFee                    string // i128 canonical-decimals
	HookData                  string // hex; opaque post-mint payload
}

// MintAndWithdraw is the canonical projection of one
// `MintAndWithdraw` event from TokenMessengerMinter (v2).
//
// Source schema:
//
//	pub struct MintAndWithdraw {
//	    #[topic] pub mint_recipient: Address,
//	    pub amount: i128,
//	    #[topic] pub mint_token: Address,
//	    pub fee_collected: i128,
//	}
//
// Wire shape:
//
//	topics = ["mint_and_withdraw", mint_recipient, mint_token]
//	body   = ScMap { amount, fee_collected }
type MintAndWithdraw struct {
	Ledger     uint32
	TxHash     string
	OpIndex    int
	ClosedAt   string
	ContractID string

	MintRecipient string // Stellar Address strkey
	MintToken     string // Stellar Address strkey

	Amount       string // i128
	FeeCollected string // i128
}

// MessageSent is the canonical projection of one `MessageSent`
// event from MessageTransmitter (v2). Emitted alongside
// `DepositForBurn` for every outbound transfer.
//
// Source schema:
//
//	pub struct MessageSent {
//	    pub message: Bytes,
//	}
//
// Wire shape:
//
//	topics = ["message_sent"]   (single-topic event)
//	body   = Bytes (raw)
//
// The `message` bytes are the serialised cross-chain envelope —
// destination chain attestation services consume this; we
// preserve it as hex for cross-reference.
type MessageSent struct {
	Ledger     uint32
	TxHash     string
	OpIndex    int
	ClosedAt   string
	ContractID string

	Message string // hex of the serialised envelope
}

// MessageReceived is the canonical projection of one
// `MessageReceived` event from MessageTransmitter (v2). Emitted
// alongside `MintAndWithdraw` for every inbound transfer.
//
// Source schema:
//
//	pub struct MessageReceived {
//	    #[topic] pub caller: Address,
//	    pub source_domain: u32,
//	    #[topic] pub nonce: BytesN<32>,
//	    pub sender: BytesN<32>,
//	    #[topic] pub finality_threshold_executed: u32,
//	    pub message_body: Bytes,
//	}
//
// Wire shape:
//
//	topics = ["message_received", caller, nonce, finality_threshold_executed]
//	body   = ScMap { source_domain, sender, message_body }
type MessageReceived struct {
	Ledger     uint32
	TxHash     string
	OpIndex    int
	ClosedAt   string
	ContractID string

	// Topics
	Caller                    string // Stellar Address strkey
	Nonce                     string // hex; BytesN<32>
	FinalityThresholdExecuted uint32

	// Body
	SourceDomain uint32 // CCTP domain ID
	Sender       string // hex; BytesN<32>
	MessageBody  string // hex
}
