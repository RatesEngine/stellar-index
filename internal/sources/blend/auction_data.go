package blend

import (
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// AuctionData wire-format keys. Verified against
// pool/src/auctions/auction.rs::AuctionData. soroban-sdk
// #[contracttype] structs serialise as ScvMap with sorted-by-symbol
// keys; we look up by name regardless of order to stay resilient
// to declaration reorderings (per
// docs/architecture/contract-schema-evolution.md).
const (
	auctionDataKeyBid   = "bid"
	auctionDataKeyLot   = "lot"
	auctionDataKeyBlock = "block"
)

// decodeAuctionData parses the AuctionData ScvMap embedded in
// new_auction / fill_auction event bodies into the AuctionData
// struct.
//
// AuctionData on the wire (from blend-contracts-v2
// pool/src/auctions/auction.rs):
//
//	ScvMap with three named fields:
//	  "bid"   → ScvMap{ Address → i128 }   — assets the filler spends
//	  "block" → u32                         — auction-start block
//	  "lot"   → ScvMap{ Address → i128 }   — assets the filler receives
//
// Decoder is by name (resilient to field reordering) and accepts
// both Soroban contract addresses and account addresses for the
// asset key — Blend asset registries today are all Soroban
// contracts (SAC-wrapped classic + per-token Soroban tokens) but
// the asset-key parse stays generous to future-proof.
func decodeAuctionData(sv scval.ScVal) (AuctionData, error) {
	entries, err := scval.AsMap(sv)
	if err != nil {
		return AuctionData{}, fmt.Errorf("auction_data shape: %w", err)
	}

	bidEntry, ok := scval.MapField(entries, auctionDataKeyBid)
	if !ok {
		return AuctionData{}, fmt.Errorf("auction_data missing %q", auctionDataKeyBid)
	}
	bid, err := decodeAssetAmountMap(bidEntry, auctionDataKeyBid)
	if err != nil {
		return AuctionData{}, err
	}

	lotEntry, ok := scval.MapField(entries, auctionDataKeyLot)
	if !ok {
		return AuctionData{}, fmt.Errorf("auction_data missing %q", auctionDataKeyLot)
	}
	lot, err := decodeAssetAmountMap(lotEntry, auctionDataKeyLot)
	if err != nil {
		return AuctionData{}, err
	}

	blockEntry, ok := scval.MapField(entries, auctionDataKeyBlock)
	if !ok {
		return AuctionData{}, fmt.Errorf("auction_data missing %q", auctionDataKeyBlock)
	}
	block, err := scval.AsU32(blockEntry)
	if err != nil {
		return AuctionData{}, fmt.Errorf("%s: %w", auctionDataKeyBlock, err)
	}

	return AuctionData{Bid: bid, Lot: lot, Block: block}, nil
}

// decodeAssetAmountMap decodes one of the inner Map<Address, i128>
// values inside AuctionData. Stable order is the on-wire ScMap
// order — we don't sort because callers (e.g. storage layer) may
// want to preserve the chain-emitted order for round-trip parity.
func decodeAssetAmountMap(sv scval.ScVal, fieldName string) ([]AssetAmount, error) {
	entries, err := scval.AsMap(sv)
	if err != nil {
		return nil, fmt.Errorf("%s: shape: %w", fieldName, err)
	}
	out := make([]AssetAmount, 0, len(entries))
	for i := range entries {
		strkey, err := scval.AsAddressStrkey(entries[i].Key)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] key: %w", fieldName, i, err)
		}
		asset, err := canonical.NewSorobanAsset(strkey)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] asset: %w", fieldName, i, err)
		}
		amount, err := scval.AsAmountFromI128(entries[i].Val)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] amount: %w", fieldName, i, err)
		}
		out = append(out, AssetAmount{Asset: asset, Amount: amount.BigInt()})
	}
	return out, nil
}
