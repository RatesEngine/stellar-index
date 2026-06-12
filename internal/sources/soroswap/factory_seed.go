package soroswap

import (
	"context"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/scval"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// seedThrottle is the delay inserted between simulateTransaction
// calls during a factory sweep. Public stellar-rpc endpoints (e.g.
// mainnet.sorobanrpc.com behind Cloudflare) return 429 above
// ~3-5 req/s even on cached simulate responses. 300ms keeps us
// comfortably under that. For a 200-pair factory the full sweep
// takes ~3min of wall time — acceptable at boot and verify time.
// Behind an unthrottled endpoint this is slow; a follow-up could
// parallelise across a small worker pool or swap for paid-tier
// throughput.
const seedThrottle = 300 * time.Millisecond

// SeedFromFactoryRPC populates the Decoder's pair→(token0, token1)
// registry by reading the Soroswap factory's on-chain state via
// stellar-rpc simulateTransaction.
//
// The flow is the three-step sweep the factory exposes via view
// functions:
//
//  1. factory.all_pairs_length() -> u32  → N
//  2. factory.all_pairs(i) -> Address     → pair_i for i in [0, N)
//  3. pair_i.token_0() + pair_i.token_1() -> Address → token identities
//
// Each simulateTransaction round-trip runs the contract function
// locally on the RPC node and returns the SCVal result; no ledger
// state is changed, no fee is paid. Typical factory size on pubnet
// is a few hundred pairs, so the full sweep is ~3N+1 RPC calls —
// seconds of wall time at mild concurrency.
//
// Cold-start use case: the live dispatcher path records every future
// new_pair event on the fly (see Decoder.SeedPair, fired from Decode
// on each factory new_pair event), but pairs created BEFORE the
// dispatcher's first ledger are invisible to live events. This method
// fills that gap. Once seeded, the live path keeps the registry in
// sync automatically.
//
// factoryContract is the C-strkey of the Soroswap factory. For
// mainnet that's CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2.
//
// Returns the number of pairs seeded and a non-nil error on any
// failure — the caller decides whether to fail-closed (refuse to
// start the dispatcher) or fail-open (log and continue with an
// empty registry, accept silent swaps). Idempotent: seeded entries
// overwrite existing ones, so re-running is safe.
func (d *Decoder) SeedFromFactoryRPC(ctx context.Context, rpc *stellarrpc.Client, factoryContract string) (int, error) {
	length, err := callU32(ctx, rpc, factoryContract, "all_pairs_length", nil)
	if err != nil {
		return 0, fmt.Errorf("soroswap seed: all_pairs_length: %w", err)
	}

	seeded := 0
	for i := uint32(0); i < length; i++ {
		time.Sleep(seedThrottle)
		pairAddr, err := callAddressStrkey(ctx, rpc, factoryContract, "all_pairs",
			[]scval.ScVal{scval.NewU32(i)})
		if err != nil {
			return seeded, fmt.Errorf("soroswap seed: all_pairs(%d): %w", i, err)
		}

		time.Sleep(seedThrottle)
		token0Addr, err := callAddressStrkey(ctx, rpc, pairAddr, "token_0", nil)
		if err != nil {
			return seeded, fmt.Errorf("soroswap seed: pair %s token_0: %w", pairAddr, err)
		}
		time.Sleep(seedThrottle)
		token1Addr, err := callAddressStrkey(ctx, rpc, pairAddr, "token_1", nil)
		if err != nil {
			return seeded, fmt.Errorf("soroswap seed: pair %s token_1: %w", pairAddr, err)
		}

		token0, err := canonical.NewSorobanAsset(token0Addr)
		if err != nil {
			return seeded, fmt.Errorf("soroswap seed: pair %s token0 %s: %w", pairAddr, token0Addr, err)
		}
		token1, err := canonical.NewSorobanAsset(token1Addr)
		if err != nil {
			return seeded, fmt.Errorf("soroswap seed: pair %s token1 %s: %w", pairAddr, token1Addr, err)
		}

		d.SeedPair(pairAddr, token0, token1)
		seeded++
	}
	return seeded, nil
}

// callU32 invokes a u32-returning view function via simulateTransaction.
func callU32(ctx context.Context, rpc *stellarrpc.Client, contract, fn string, args []scval.ScVal) (uint32, error) {
	sv, err := callView(ctx, rpc, contract, fn, args)
	if err != nil {
		return 0, err
	}
	return scval.AsU32(sv)
}

// callAddressStrkey invokes an Address-returning view function and
// returns the address as a C-strkey (contract) or G-strkey (account).
func callAddressStrkey(ctx context.Context, rpc *stellarrpc.Client, contract, fn string, args []scval.ScVal) (string, error) {
	sv, err := callView(ctx, rpc, contract, fn, args)
	if err != nil {
		return "", err
	}
	return scval.AsAddressStrkey(sv)
}

// callView is the common simulateTransaction machinery: builds the
// envelope, submits, parses the result SCVal.
func callView(ctx context.Context, rpc *stellarrpc.Client, contract, fn string, args []scval.ScVal) (scval.ScVal, error) {
	b64, err := stellarrpc.InvokeContractTxEnvelope("", contract, fn, args)
	if err != nil {
		return scval.ScVal{}, fmt.Errorf("build envelope: %w", err)
	}
	resp, err := rpc.SimulateTransaction(ctx, b64)
	if err != nil {
		return scval.ScVal{}, fmt.Errorf("simulate: %w", err)
	}
	if resp.Error != "" {
		return scval.ScVal{}, fmt.Errorf("simulate rejected: %s", resp.Error)
	}
	if len(resp.Results) == 0 {
		return scval.ScVal{}, fmt.Errorf("simulate returned no results")
	}
	return scval.Parse(resp.Results[0].XDR)
}
