//go:build unit
// +build unit

/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package gocql

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestRowMapBytesFastPath(t *testing.T) {
	src := []byte{1, 2, 3, 4}
	var nilBlob []byte
	emptyBlob := []byte{}
	other := []any{"other"}
	rd := &RowData{
		Columns: []string{"blob_col", "nil_blob_col", "empty_blob_col", "other_col"},
		Values:  []any{&src, &nilBlob, &emptyBlob, &other},
	}

	m := make(map[string]any, len(rd.Columns))
	rd.rowMap(m)

	got, ok := m["blob_col"].([]byte)
	if !ok {
		t.Fatalf("blob_col: expected []byte, got %T", m["blob_col"])
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("blob_col: content mismatch, got %v want %v", got, src)
	}
	if len(got) > 0 && &got[0] == &src[0] {
		t.Fatal("blob_col: expected a defensive copy, but backing array is shared with source")
	}
	// Mutating the copy must not affect the original source slice.
	got[0] = 99
	if src[0] == 99 {
		t.Fatal("blob_col: mutating the returned copy mutated the source slice")
	}

	// A nil []byte column comes back as a typed-nil []byte inside the
	// interface (matching the pre-existing reflect-path behavior for nil
	// slices), not an untyped nil.
	nilGot, ok := m["nil_blob_col"].([]byte)
	if !ok || nilGot != nil {
		t.Fatalf("nil_blob_col: expected a nil []byte, got %#v", m["nil_blob_col"])
	}

	// A non-nil, empty []byte column must come back as a distinct non-nil
	// empty slice (not aliased to the source, not collapsed to nil) --
	// exercising the b != nil branch with a zero-length slice.
	emptyGot, ok := m["empty_blob_col"].([]byte)
	if !ok || emptyGot == nil || len(emptyGot) != 0 {
		t.Fatalf("empty_blob_col: expected a non-nil, empty []byte, got %#v", m["empty_blob_col"])
	}

	gotOther, ok := m["other_col"].([]any)
	if !ok || len(gotOther) != 1 || gotOther[0] != "other" {
		t.Fatalf("other_col: unexpected value %#v", m["other_col"])
	}
	// The generic reflect path must also return a defensive copy: mutating
	// it must not affect the original source slice.
	gotOther[0] = "mutated"
	if other[0] != "other" {
		t.Fatal("other_col: mutating the returned copy mutated the source slice")
	}
}

// assertResolvedType compares got against want, except for a VectorType's
// NativeType: that one carries the bare VectorType prefix instead of the column's
// full spec, which is #1085. Pinning it here would make closing that issue an edit
// to these tests. Everything the guards under test actually decide -- the element
// type and the dimensions -- is still compared exactly.
func assertResolvedType(t *testing.T, typ string, got, want TypeInfo) {
	t.Helper()

	wantVec, ok := want.(VectorType)
	if !ok {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("getCassandraLongType(%q) = %#v, want %#v", typ, got, want)
		}
		return
	}

	gotVec, ok := got.(VectorType)
	if !ok {
		t.Fatalf("getCassandraLongType(%q) = %#v, want a VectorType", typ, got)
	}
	if !reflect.DeepEqual(gotVec.SubType, wantVec.SubType) || gotVec.Dimensions != wantVec.Dimensions {
		t.Errorf("getCassandraLongType(%q) resolved to element type %#v with %d dimensions, want %#v with %d",
			typ, gotVec.SubType, gotVec.Dimensions, wantVec.SubType, wantVec.Dimensions)
	}
}

// TestGetCassandraLongTypeRejectsShortSplits pins that a composite type name with
// a missing or truncated argument list degrades to an opaque custom type instead
// of panicking. The well-formed names keep the degrade path from swallowing them.
func TestGetCassandraLongTypeRejectsShortSplits(t *testing.T) {
	t.Parallel()

	const p = apacheCassandraTypePrefix

	tests := []struct {
		name string
		typ  string
		want TypeInfo
	}{
		{name: "vector without arguments", typ: p + "VectorType", want: NewCustomType(0, TypeCustom, p+"VectorType")},
		{name: "vector without dimensions", typ: p + "VectorType(" + p + "FloatType)", want: NewCustomType(0, TypeCustom, p+"VectorType("+p+"FloatType)")},
		{name: "udt without arguments", typ: p + "UserType", want: NewCustomType(0, TypeCustom, p+"UserType")},
		{name: "udt without a name", typ: p + "UserType(gocql_test)", want: NewCustomType(0, TypeCustom, p+"UserType(gocql_test)")},
		{name: "udt field without a type", typ: p + "UserType(gocql_test,706572736f6e,616765)", want: NewCustomType(0, TypeCustom, p+"UserType(gocql_test,706572736f6e,616765)")},
		{name: "vector without an element type", typ: p + "VectorType(, 3)", want: NewCustomType(0, TypeCustom, p+"VectorType(, 3)")},
		{
			// The outer vector is well-formed; only its element type degrades.
			name: "vector whose element has no element type",
			typ:  p + "VectorType(" + p + "VectorType(, 3), 2)",
			want: VectorType{
				SubType:    NewCustomType(0, TypeCustom, p+"VectorType(, 3)"),
				Dimensions: 2,
			},
		},
		{
			name: "vector of float",
			typ:  p + "VectorType(" + p + "FloatType, 3)",
			want: VectorType{
				SubType:    NewNativeType(0, TypeFloat),
				Dimensions: 3,
			},
		},
		{
			name: "udt",
			typ:  p + "UserType(gocql_test,706572736f6e,616765:" + p + "Int32Type)",
			want: UDTTypeInfo{
				NativeType: NewNativeType(0, TypeUDT),
				KeySpace:   "gocql_test",
				Name:       "person",
				Elements:   []UDTField{{Name: "age", Type: NewNativeType(0, TypeInt)}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assertResolvedType(t, test.typ, getCassandraLongType(test.typ, 0, nopLogger{}), test.want)
		})
	}
}

// TestGetCassandraLongTypeRejectsNonPositiveDimensions pins that a vector
// dimension must be positive. A nested vector is where a negative one gets in,
// since readVectorTypeInfo validates only the outer spec.
func TestGetCassandraLongTypeRejectsNonPositiveDimensions(t *testing.T) {
	t.Parallel()

	const p = apacheCassandraTypePrefix

	tests := []struct {
		name string
		typ  string
		want TypeInfo
	}{
		{name: "negative dimensions", typ: p + "VectorType(" + p + "FloatType, -3)", want: NewCustomType(0, TypeCustom, p+"VectorType("+p+"FloatType, -3)")},
		{name: "zero dimensions", typ: p + "VectorType(" + p + "FloatType, 0)", want: NewCustomType(0, TypeCustom, p+"VectorType("+p+"FloatType, 0)")},
		{
			// The outer vector is well-formed; only its element type degrades.
			name: "negative dimensions nested in a valid vector",
			typ:  p + "VectorType(" + p + "VectorType(" + p + "FloatType, -3), 2)",
			want: VectorType{
				SubType:    NewCustomType(0, TypeCustom, p+"VectorType("+p+"FloatType, -3)"),
				Dimensions: 2,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assertResolvedType(t, test.typ, getCassandraLongType(test.typ, 0, nopLogger{}), test.want)
		})
	}
}

// TestAsVectorTypeRejectsNonPositiveDimensions covers the third parser of the same
// grammar, which backs MapScan and SliceMap through goType.
func TestAsVectorTypeRejectsNonPositiveDimensions(t *testing.T) {
	t.Parallel()

	const p = apacheCassandraTypePrefix

	tests := []struct {
		name    string
		typ     string
		wantOK  bool
		wantDim int
	}{
		{name: "negative dimensions", typ: p + "VectorType(" + p + "FloatType, -3)"},
		{name: "zero dimensions", typ: p + "VectorType(" + p + "FloatType, 0)"},
		{name: "vector of float", typ: p + "VectorType(" + p + "FloatType, 3)", wantOK: true, wantDim: 3},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, ok := asVectorType(NewCustomType(protoVersion4, TypeCustom, test.typ))
			if ok != test.wantOK {
				t.Fatalf("asVectorType(%q) ok = %t, want %t (got %#v)", test.typ, ok, test.wantOK, got)
			}
			if ok && got.Dimensions != test.wantDim {
				t.Errorf("asVectorType(%q).Dimensions = %d, want %d", test.typ, got.Dimensions, test.wantDim)
			}
		})
	}
}

// TestGetCassandraLongTypeLogsWhyADimensionWasRejected pins the reason, not just the
// rejection, so that a caller passing a real logger can tell the two apart.
func TestGetCassandraLongTypeLogsWhyADimensionWasRejected(t *testing.T) {
	t.Parallel()

	const p = apacheCassandraTypePrefix

	tests := []struct {
		name string
		typ  string
		want string
	}{
		{name: "not a number", typ: p + "VectorType(" + p + "FloatType, abc)", want: "strconv.Atoi"},
		{name: "negative dimensions", typ: p + "VectorType(" + p + "FloatType, -3)", want: "expecting a positive value"},
		{name: "zero dimensions", typ: p + "VectorType(" + p + "FloatType, 0)", want: "expecting a positive value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			logger := &testLogger{}
			getCassandraLongType(test.typ, 0, logger)

			got := logger.String()
			if !strings.Contains(got, test.want) {
				t.Errorf("getCassandraLongType(%q) logged %q, want it to contain %q", test.typ, got, test.want)
			}
			if strings.Contains(got, "<nil>") {
				t.Errorf("getCassandraLongType(%q) logged %q, which reports no reason", test.typ, got)
			}
		})
	}
}

func nestedJavaListType(levels int) string {
	const p = apacheCassandraTypePrefix
	var b strings.Builder
	b.Grow(levels*(len(p)+len("ListType()")) + len(p) + len("Int32Type"))
	for i := 0; i < levels; i++ {
		b.WriteString(p)
		b.WriteString("ListType(")
	}
	b.WriteString(p)
	b.WriteString("Int32Type")
	for i := 0; i < levels; i++ {
		b.WriteByte(')')
	}
	return b.String()
}

func nestedJavaFrozenType(levels int) string {
	const p = apacheCassandraTypePrefix
	var b strings.Builder
	b.Grow(levels*(len(p)+len("FrozenType()")) + len(p) + len("Int32Type"))
	for i := 0; i < levels; i++ {
		b.WriteString(p)
		b.WriteString("FrozenType(")
	}
	b.WriteString(p)
	b.WriteString("Int32Type")
	for i := 0; i < levels; i++ {
		b.WriteByte(')')
	}
	return b.String()
}

func nestedJavaVectorType(levels int) string {
	const p = apacheCassandraTypePrefix
	var b strings.Builder
	b.Grow(levels*(len(p)+len("VectorType(, 1)")) + len(p) + len("FloatType"))
	for i := 0; i < levels; i++ {
		b.WriteString(p)
		b.WriteString("VectorType(")
	}
	b.WriteString(p)
	b.WriteString("FloatType")
	for i := 0; i < levels; i++ {
		b.WriteString(", 1)")
	}
	return b.String()
}

func innermostCompositeElem(t TypeInfo) TypeInfo {
	for {
		switch v := t.(type) {
		case CollectionType:
			t = v.Elem
		case VectorType:
			t = v.SubType
		default:
			return t
		}
	}
}

// TestGetCassandraLongTypeRejectsExcessiveNesting pins that a Java type name
// nested past maxTypeInfoDepth degrades instead of overflowing the stack. The
// matching binary parser in readTypeInfo panics with a malformedFrameError;
// this parser has no recover, so a too-deep name stays an opaque custom type.
func TestGetCassandraLongTypeRejectsExcessiveNesting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  string
	}{
		{name: "list", typ: nestedJavaListType(maxTypeInfoDepth + 1)},
		{name: "frozen", typ: nestedJavaFrozenType(maxTypeInfoDepth + 1)},
		{name: "vector", typ: nestedJavaVectorType(maxTypeInfoDepth + 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			logger := &testLogger{}
			got := getCassandraLongType(test.typ, 0, logger)
			if !strings.Contains(logger.String(), "nesting exceeds") {
				t.Errorf("getCassandraLongType logged %q, want it to contain the nesting limit", logger.String())
			}

			leaf := innermostCompositeElem(got)
			if leaf.Type() != TypeCustom {
				t.Errorf("innermost type is %s (%#v), want custom after the nesting limit", leaf.Type(), leaf)
			}

			// asVectorType re-enters the same parser; it must also stop short of a stack overflow.
			_, _ = asVectorType(NewCustomType(0, TypeCustom, test.typ))
		})
	}
}

// TestGetCassandraLongTypeAcceptsMaxNesting pins that the bound did not reject
// a Java list type nested exactly as deep as the binary parser allows.
func TestGetCassandraLongTypeAcceptsMaxNesting(t *testing.T) {
	t.Parallel()

	logger := &testLogger{}
	got := getCassandraLongType(nestedJavaListType(maxTypeInfoDepth), 0, logger)
	if log := logger.String(); log != "" {
		t.Errorf("getCassandraLongType logged %q for a type at the nesting limit", log)
	}
	n, leaf := nestedCollectionCount(got)
	if n != maxTypeInfoDepth {
		t.Fatalf("nested %d lists, want %d", n, maxTypeInfoDepth)
	}
	if leaf.Type() != TypeInt {
		t.Fatalf("innermost type is %s, want int", leaf.Type())
	}
}
