package config

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// TestDefault_MatchesStructTags is the backstop for F-1327: the `default:`
// struct tags are documentation-only (they feed the generated config
// reference via schema.go) — the REAL runtime defaults come from
// Default(). When the two drift, operators read one value in the docs and
// get another at runtime. That drift is how `persist_per_source` ended up
// documented `true` (Phase-3 parallel, the safe value) while Default()
// left it the zero-value `false` (Phase-4 sole-writer) — enabling the
// projector "per the docs" would have silently dropped the entire SEP-41
// domain (F-1316).
//
// This test walks every leaf field that carries a `default:` tag and
// asserts Default() produces exactly that value. Keep Default() and the
// tags in lockstep; a new `default:"…"` tag MUST be matched by a
// Default() assignment (or this test fails).
func TestDefault_MatchesStructTags(t *testing.T) {
	checkDefaultTags(t, reflect.ValueOf(Default()), "")
}

func checkDefaultTags(t *testing.T, v reflect.Value, prefix string) {
	t.Helper()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		fv := v.Field(i)
		name := prefix + f.Name

		// Recurse into nested config structs (time.Duration is a defined
		// int64, not a struct, so it falls through to the leaf check).
		if fv.Kind() == reflect.Struct {
			checkDefaultTags(t, fv, name+".")
		}

		tag, ok := f.Tag.Lookup("default")
		if !ok {
			continue
		}
		if !defaultTagMatches(fv, tag) {
			t.Errorf("%s: Default() = %#v but default tag = %q — keep Default() and the doc tag in lockstep (F-1327)",
				name, fv.Interface(), tag)
		}
	}
}

func defaultTagMatches(fv reflect.Value, tag string) bool {
	// An empty tag (`default:""`) documents "no explicit default — the
	// consumption site supplies a fallback". It matches the field's zero
	// value (e.g. a time.Duration PollInterval left 0).
	if tag == "" {
		return fv.IsZero()
	}
	// time.Duration is stored as int64 nanoseconds but tagged as a
	// duration string ("15m") — handle it before the generic int path.
	if fv.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(tag)
		return err == nil && time.Duration(fv.Int()) == d
	}

	switch fv.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(tag)
		return err == nil && fv.Bool() == b
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(tag, 10, 64)
		return err == nil && fv.Int() == n
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(tag, 10, 64)
		return err == nil && fv.Uint() == n
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(tag, 64)
		return err == nil && fv.Float() == f
	case reflect.String:
		return fv.String() == tag
	case reflect.Slice:
		// []string tags use JSON array syntax ("[]" or ["a","b"]).
		if fv.Type().Elem().Kind() != reflect.String {
			return true // non-string slices aren't tag-defaulted
		}
		var want []string
		if err := json.Unmarshal([]byte(tag), &want); err != nil {
			return false
		}
		got, _ := fv.Interface().([]string)
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	default:
		// Maps / pointers / structs aren't scalar-tag-defaulted; treat as
		// out of scope rather than a failure.
		return true
	}
}
