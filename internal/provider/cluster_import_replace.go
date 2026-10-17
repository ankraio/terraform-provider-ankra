// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

// clusterImportedKey marks, in private state, a provisioned cluster that was
// imported and whose creation arguments have not been adopted yet. The
// platform does not return a cluster's build arguments, so an imported
// cluster's state holds them as null; the first apply adopts the configured
// values in place instead of replacing the cluster.
const clusterImportedKey = "cluster_imported"

// clusterImportedMarker is the value stored under clusterImportedKey.
var clusterImportedMarker = []byte(`{"imported":true}`)

const clusterReplaceDescription = "The platform cannot change this on an existing cluster, so changing it replaces the cluster. " +
	"Right after an import the configured value is adopted without a replacement."

type clusterPrivateReader interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
}

type clusterPrivateWriter interface {
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

// clusterRequiresReplace decides a replacement for a changed creation
// argument: always, except when the state holds no value because the
// cluster was just imported.
func clusterRequiresReplace(ctx context.Context, stateIsNull bool, private clusterPrivateReader) bool {
	if !stateIsNull {
		return true
	}
	marker, _ := private.GetKey(ctx, clusterImportedKey)
	return len(marker) == 0
}

// markClusterImported records that the cluster was just imported.
func markClusterImported(ctx context.Context, private clusterPrivateWriter) diag.Diagnostics {
	return private.SetKey(ctx, clusterImportedKey, clusterImportedMarker)
}

// clearClusterImported ends the adoption window once an apply has written
// the configured creation arguments to state.
func clearClusterImported(ctx context.Context, private clusterPrivateWriter) diag.Diagnostics {
	return private.SetKey(ctx, clusterImportedKey, nil)
}

func clusterReplaceString() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = clusterRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, clusterReplaceDescription, clusterReplaceDescription)
}

func clusterReplaceInt64() planmodifier.Int64 {
	return int64planmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.Int64Request, response *int64planmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = clusterRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, clusterReplaceDescription, clusterReplaceDescription)
}

func clusterReplaceBool() planmodifier.Bool {
	return boolplanmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.BoolRequest, response *boolplanmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = clusterRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, clusterReplaceDescription, clusterReplaceDescription)
}

func clusterReplaceList() planmodifier.List {
	return listplanmodifier.RequiresReplaceIf(func(ctx context.Context, request planmodifier.ListRequest, response *listplanmodifier.RequiresReplaceIfFuncResponse) {
		response.RequiresReplace = clusterRequiresReplace(ctx, request.StateValue.IsNull(), request.Private)
	}, clusterReplaceDescription, clusterReplaceDescription)
}
