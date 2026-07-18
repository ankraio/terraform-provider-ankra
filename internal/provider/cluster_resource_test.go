// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStacksToAPIMapsNestedBlocks(t *testing.T) {
	t.Parallel()

	stacks := []stackModel{
		{
			Name:        types.StringValue("platform"),
			Description: types.StringValue("core stack"),
			Manifests: []manifestModel{
				{
					Name:           types.StringValue("namespace"),
					Namespace:      types.StringValue("test-ns"),
					ManifestBase64: types.StringValue("YmFzZTY0"),
					Parents:        []types.String{types.StringValue("root")},
					FromFile:       types.StringNull(),
				},
			},
			Addons: []addonModel{
				{
					Name:          types.StringValue("ingress"),
					ChartName:     types.StringValue("ingress-nginx"),
					ChartVersion:  types.StringValue("4.0.0"),
					RepositoryURL: types.StringValue("https://example.com"),
					Namespace:     types.StringValue("ingress"),
				},
			},
		},
	}

	result := stacksToAPI(stacks)
	if len(result) != 1 {
		t.Fatalf("expected 1 stack, got %d", len(result))
	}
	stack := result[0]
	if stack.Name != "platform" || stack.Description != "core stack" {
		t.Errorf("unexpected stack fields: %+v", stack)
	}
	if len(stack.Manifests) != 1 || stack.Manifests[0].ManifestBase64 != "YmFzZTY0" {
		t.Errorf("unexpected manifests: %+v", stack.Manifests)
	}
	if len(stack.Manifests[0].Parents) != 1 || stack.Manifests[0].Parents[0] != "root" {
		t.Errorf("unexpected parents: %+v", stack.Manifests[0].Parents)
	}
	if len(stack.Addons) != 1 || stack.Addons[0].ChartName != "ingress-nginx" {
		t.Errorf("unexpected addons: %+v", stack.Addons)
	}
}

func TestStacksToAPIEmpty(t *testing.T) {
	t.Parallel()

	if result := stacksToAPI(nil); len(result) != 0 {
		t.Errorf("expected empty result, got %+v", result)
	}
}

func TestStringsToAPINilForEmpty(t *testing.T) {
	t.Parallel()

	if result := stringsToAPI(nil); result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}
