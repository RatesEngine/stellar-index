// Tiny dev helper: print the structural decomposition of a base64-
// encoded SCVal. Used when a decoder errors on a real on-wire event
// and we need to see the actual wire shape.
//
// Usage:
//
//	go run scripts/dev/decode-scval <base64-scval>
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: decode-scval <base64>")
		os.Exit(2)
	}
	raw, err := base64.StdEncoding.DecodeString(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "base64:", err)
		os.Exit(1)
	}
	var sv xdr.ScVal
	if err := sv.UnmarshalBinary(raw); err != nil {
		fmt.Fprintln(os.Stderr, "unmarshal:", err)
		os.Exit(1)
	}
	printScVal(sv, 0)
}

func printScVal(sv xdr.ScVal, depth int) {
	indent := strings.Repeat("  ", depth)
	switch sv.Type {
	case xdr.ScValTypeScvBytes:
		fmt.Printf("%sBytes(%d): %x\n", indent, len(*sv.Bytes), *sv.Bytes)
		// Also try to re-parse the inner bytes as SCVal.
		var inner xdr.ScVal
		if err := inner.UnmarshalBinary(*sv.Bytes); err == nil {
			fmt.Printf("%s=> inner SCVal when re-parsed:\n", indent)
			printScVal(inner, depth+1)
		}
	case xdr.ScValTypeScvMap:
		if sv.Map == nil {
			fmt.Printf("%sMap(nil)\n", indent)
			return
		}
		fmt.Printf("%sMap(%d entries):\n", indent, len(**sv.Map))
		for _, e := range **sv.Map {
			fmt.Printf("%s  key:\n", indent)
			printScVal(e.Key, depth+2)
			fmt.Printf("%s  val:\n", indent)
			printScVal(e.Val, depth+2)
		}
	case xdr.ScValTypeScvVec:
		if sv.Vec == nil {
			fmt.Printf("%sVec(nil)\n", indent)
			return
		}
		fmt.Printf("%sVec(%d items):\n", indent, len(**sv.Vec))
		for i, item := range **sv.Vec {
			fmt.Printf("%s  [%d]\n", indent, i)
			printScVal(item, depth+2)
		}
	case xdr.ScValTypeScvSymbol:
		fmt.Printf("%sSymbol(%q)\n", indent, string(*sv.Sym))
	case xdr.ScValTypeScvString:
		fmt.Printf("%sString(%q)\n", indent, string(*sv.Str))
	case xdr.ScValTypeScvU32:
		fmt.Printf("%sU32(%d)\n", indent, *sv.U32)
	case xdr.ScValTypeScvU64:
		fmt.Printf("%sU64(%d)\n", indent, *sv.U64)
	case xdr.ScValTypeScvAddress:
		if sv.Address != nil {
			s, _ := sv.Address.String()
			fmt.Printf("%sAddress(%s)\n", indent, s)
		}
	default:
		fmt.Printf("%s%s: %+v\n", indent, sv.Type.String(), sv)
	}
}
