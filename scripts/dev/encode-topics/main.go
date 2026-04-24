// Tiny dev helper: print base64-encoded SCVal::Symbol or SCVal::String
// bytes for a list of topic strings. Used when writing or reviewing
// capture scripts where the shell needs hardcoded topic blobs.
//
// Usage:
//
//	go run scripts/dev/encode-topics [-type symbol|string] <name> [<name>…]
//
// Default type is symbol. Use `-type string` for contracts that emit
// String-typed topic slots (e.g. Soroswap — `("SoroswapPair", …)`
// where the first tuple element is a Rust string literal, serialized
// as ScvString, not ScvSymbol).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

func main() {
	kind := flag.String("type", "symbol", "encoding: symbol | string")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: encode-topics [-type symbol|string] <name> [<name>…]")
		os.Exit(2)
	}
	for _, s := range flag.Args() {
		var b64 string
		var err error
		switch *kind {
		case "symbol":
			b64, err = scval.EncodeSymbol(s)
		case "string":
			b64, err = scval.EncodeString(s)
		default:
			fmt.Fprintf(os.Stderr, "unknown -type %q (want symbol|string)\n", *kind)
			os.Exit(2)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode %q: %v\n", s, err)
			os.Exit(1)
		}
		fmt.Printf("%-20s %s\n", s, b64)
	}
}
