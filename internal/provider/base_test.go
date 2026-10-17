// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOptionalValueConversions(t *testing.T) {
	t.Parallel()

	if boolPointer(types.BoolNull()) != nil || boolPointer(types.BoolUnknown()) != nil {
		t.Error("null and unknown booleans must be omitted")
	}
	if flag := boolPointer(types.BoolValue(false)); flag == nil || *flag {
		t.Error("an explicit false must be sent, not omitted")
	}
	if int64Pointer(types.Int64Null()) != nil {
		t.Error("a null integer must be omitted")
	}
	if number := int64Pointer(types.Int64Value(0)); number == nil || *number != 0 {
		t.Error("an explicit zero must be sent, not omitted")
	}
	if !optionalString("").IsNull() {
		t.Error("an empty API string must read as null")
	}
	if optionalString("x").ValueString() != "x" {
		t.Error("a non-empty API string must read as itself")
	}
}

func TestStringListRoundTrip(t *testing.T) {
	t.Parallel()

	var diagnostics diag.Diagnostics
	if values := stringsFromList(context.Background(), types.ListNull(types.StringType), &diagnostics); values != nil {
		t.Errorf("null list read as %v", values)
	}
	if !listFromStrings(nil).IsNull() {
		t.Error("a nil slice must be a null list")
	}

	list := listFromStrings([]string{"a", "b"})
	values := stringsFromList(context.Background(), list, &diagnostics)
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	if !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Errorf("round trip gave %v", values)
	}
	if empty := listFromStrings([]string{}); empty.IsNull() || len(empty.Elements()) != 0 {
		t.Error("an empty slice must be an empty list, not null")
	}
}
