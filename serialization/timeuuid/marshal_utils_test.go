package timeuuid

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/gocql/gocql/serialization/uuid"
)

var zeroUUIDBytes = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

// Every valid string spelling of the zero UUID. With the early `s == zeroUUID`
// return gone, they must all marshal to the same 16 zero bytes, matching
// serialization/uuid for the spellings that package accepts.
var zeroUUIDSpellings = []struct {
	name     string
	in       string
	withUUID bool
}{
	{"canonical", "00000000-0000-0000-0000-000000000000", true},
	{"braces", "{00000000-0000-0000-0000-000000000000}", true},
	{"dashless", "00000000000000000000000000000000", true},
	{"urn_uuid", "urn:uuid:00000000-0000-0000-0000-000000000000", true},
	{"urn_uuid_upper", "URN:UUID:00000000-0000-0000-0000-000000000000", true},
	{"urn_timeuuid", "urn:timeuuid:00000000-0000-0000-0000-000000000000", false},
	{"urn_timeuuid_upper", "URN:TIMEUUID:00000000-0000-0000-0000-000000000000", false},
}

type customString string

func TestMarshalZeroUUIDStringSpellings(t *testing.T) {
	t.Parallel()

	for _, tc := range zeroUUIDSpellings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertMarshalsTo(t, tc.in, zeroUUIDBytes)

			if !tc.withUUID {
				if _, err := uuid.Marshal(tc.in); err == nil {
					t.Fatalf("uuid.Marshal(%q): want error; uuid does not accept urn:timeuuid:", tc.in)
				}
				return
			}
			got, err := uuid.Marshal(tc.in)
			if err != nil {
				t.Fatalf("uuid.Marshal(%q): %v", tc.in, err)
			}
			if !bytes.Equal(got, zeroUUIDBytes) {
				t.Fatalf("uuid.Marshal(%q) = %v (len %d), want 16 zero bytes", tc.in, got, len(got))
			}
		})
	}
}

func TestMarshalURNPrefixes(t *testing.T) {
	t.Parallel()

	const id = "e939f52a-d690-11ef-9cd2-0242ac120002"
	want := []byte{0xe9, 0x39, 0xf5, 0x2a, 0xd6, 0x90, 0x11, 0xef, 0x9c, 0xd2, 0x02, 0x42, 0xac, 0x12, 0x00, 0x02}

	inputs := []struct {
		name     string
		in       string
		withUUID bool
	}{
		{"urn_uuid", "urn:uuid:" + id, true},
		{"urn_uuid_upper", "URN:UUID:E939F52A-D690-11EF-9CD2-0242AC120002", true},
		{"urn_timeuuid", "urn:timeuuid:" + id, false},
		{"urn_timeuuid_upper", "URN:TIMEUUID:E939F52A-D690-11EF-9CD2-0242AC120002", false},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertMarshalsTo(t, tc.in, want)

			if !tc.withUUID {
				if _, err := uuid.Marshal(tc.in); err == nil {
					t.Fatalf("uuid.Marshal(%q): want error; uuid does not accept urn:timeuuid:", tc.in)
				}
				return
			}
			got, err := uuid.Marshal(tc.in)
			if err != nil {
				t.Fatalf("uuid.Marshal(%q): %v", tc.in, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("uuid.Marshal(%q) = %v, want %v", tc.in, got, want)
			}
		})
	}
}

func TestMarshalInvalidTimeUUIDURNPrefix(t *testing.T) {
	t.Parallel()

	// Both strings are 49 characters so they reach the urn:timeuuid: branch
	// rather than the length default. The second differs by one character so
	// a check that only looks at "urn:" would still pass it.
	invalid := []string{
		"xyz:timeuuid:00000000-0000-0000-0000-000000000000",
		"urn:timeuuiX:00000000-0000-0000-0000-000000000000",
	}
	for _, in := range invalid {
		if _, err := Marshal(in); err == nil {
			t.Errorf("Marshal(%q): want error, got nil", in)
		}
		if _, err := encString(in); err == nil {
			t.Errorf("encString(%q): want error, got nil", in)
		}
		if _, err := encReflectString(reflect.ValueOf(in)); err == nil {
			t.Errorf("encReflectString(%q): want error, got nil", in)
		}
	}
}

func assertMarshalsTo(t *testing.T, in string, want []byte) {
	t.Helper()

	checks := []struct {
		name string
		fn   func() ([]byte, error)
	}{
		{"Marshal", func() ([]byte, error) { return Marshal(in) }},
		{"EncString", func() ([]byte, error) { return EncString(in) }},
		{"encString", func() ([]byte, error) { return encString(in) }},
		{"encReflectString", func() ([]byte, error) { return encReflectString(reflect.ValueOf(in)) }},
		{"EncReflect", func() ([]byte, error) { return EncReflect(reflect.ValueOf(in)) }},
		{"Marshal customString", func() ([]byte, error) { return Marshal(customString(in)) }},
		{"EncStringR", func() ([]byte, error) {
			s := in
			return EncStringR(&s)
		}},
		{"Marshal *string", func() ([]byte, error) {
			s := in
			return Marshal(&s)
		}},
		{"Marshal *customString", func() ([]byte, error) {
			s := customString(in)
			return Marshal(&s)
		}},
	}

	for _, c := range checks {
		got, err := c.fn()
		if err != nil {
			t.Fatalf("%s(%q): %v", c.name, in, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s(%q) = %v (len %d), want %v (len %d)", c.name, in, got, len(got), want, len(want))
		}
	}
}
