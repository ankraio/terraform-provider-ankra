// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// defaultManagedPoolReplaceTimeout bounds the wait for a size change when
// the configuration sets no update timeout. The platform gives one
// replacement attempt an hour, and the new pool's nodes must come up first.
const defaultManagedPoolReplaceTimeout = 90 * time.Minute

// managedReplacementPoolName is the name ankra_managed_node_pool gives the
// pool that replaces one for a new size. It is derived from the resource's
// configured name, not from the pool being replaced, so a pool resized many
// times keeps a short name, and from the size, so a retry of the same change
// asks for the same pool, which the platform resumes. It fits the strictest
// provider rule for pool names (AKS: lowercase letters and digits, a letter
// first, at most 12 characters): up to 8 characters of the name plus 4 of a
// hash of the name and the size.
func managedReplacementPoolName(name, size string) string {
	base := make([]byte, 0, len(name))
	for _, character := range strings.ToLower(name) {
		isLowerLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		if isLowerLetter || (isDigit && len(base) > 0) {
			base = append(base, byte(character))
		}
	}
	if len(base) == 0 {
		base = []byte("pool")
	}
	if len(base) > 8 {
		base = base[:8]
	}
	digest := sha256.Sum256([]byte(name + "|" + strings.ToLower(size)))
	candidate := string(base) + hex.EncodeToString(digest[:])[:4]
	if candidate == name {
		candidate = candidate[:len(candidate)-1] + "r"
	}
	return candidate
}

// managedPoolCurrentName is the pool a state tracks: current_pool_name, or
// name for state written before current_pool_name existed (or just
// imported), when the two were always the same.
func managedPoolCurrentName(model managedNodePoolModel) string {
	if !model.CurrentPoolName.IsNull() && !model.CurrentPoolName.IsUnknown() && model.CurrentPoolName.ValueString() != "" {
		return model.CurrentPoolName.ValueString()
	}
	return model.Name.ValueString()
}

// findTrackedManagedPool finds the pool a state tracks in the listing: the
// current pool, or else a pool that replaced it for a size change this
// resource asked for and that finished after the apply stopped waiting
// (its name is the one managedReplacementPoolName gives for its size).
func findTrackedManagedPool(pools []client.ManagedNodePool, name, current string) *client.ManagedNodePool {
	for index := range pools {
		if pools[index].Name == current {
			return &pools[index]
		}
	}
	for index := range pools {
		if pools[index].Name != current && pools[index].Name == managedReplacementPoolName(name, pools[index].Size) {
			return &pools[index]
		}
	}
	return nil
}

// managedPoolCurrentNamePlanModifier plans current_pool_name: the configured
// name for a new pool, the replacement's name when the size changes (known at
// plan time, since the provider chooses it), and the recorded name otherwise.
type managedPoolCurrentNamePlanModifier struct{}

func (managedPoolCurrentNamePlanModifier) Description(context.Context) string {
	return "The pool's name, or the name of the pool that replaces it when the size changes."
}

func (modifier managedPoolCurrentNamePlanModifier) MarkdownDescription(ctx context.Context) string {
	return modifier.Description(ctx)
}

func (managedPoolCurrentNamePlanModifier) PlanModifyString(ctx context.Context, request planmodifier.StringRequest, response *planmodifier.StringResponse) {
	if request.Plan.Raw.IsNull() {
		return
	}
	var name, size types.String
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("name"), &name)...)
	response.Diagnostics.Append(request.Plan.GetAttribute(ctx, path.Root("size"), &size)...)
	if response.Diagnostics.HasError() {
		return
	}
	if request.State.Raw.IsNull() {
		response.PlanValue = name
		return
	}
	var priorName, priorSize types.String
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("name"), &priorName)...)
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("size"), &priorSize)...)
	if response.Diagnostics.HasError() {
		return
	}
	if name.IsUnknown() || size.IsUnknown() {
		response.PlanValue = types.StringUnknown()
		return
	}
	if !strings.EqualFold(size.ValueString(), priorSize.ValueString()) {
		response.PlanValue = types.StringValue(managedReplacementPoolName(name.ValueString(), size.ValueString()))
		return
	}
	if !request.StateValue.IsNull() && !request.StateValue.IsUnknown() {
		response.PlanValue = request.StateValue
		return
	}
	response.PlanValue = priorName
}

// isManagedPoolReplacementInProgress reports whether the platform refused a
// replacement because an earlier one of the pool is still running.
func isManagedPoolReplacementInProgress(err error) bool {
	var apiError *client.APIError
	return errors.As(err, &apiError) && apiError.StatusCode == http.StatusConflict &&
		strings.Contains(strings.ToLower(apiError.Body), "in progress")
}

const managedPoolNameReplaceDescription = "Changing the name replaces the pool, except to the name a pool imported by " +
	"its replacement's name was created with."

// managedPoolNameRequiresReplace lets a pool imported by the name of a
// replacement this resource created take the configured name in place: the
// recorded pool is exactly the replacement the configured name derives for
// its size.
func managedPoolNameRequiresReplace(ctx context.Context, request planmodifier.StringRequest, response *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	response.RequiresReplace = true
	if request.PlanValue.IsUnknown() || request.PlanValue.IsNull() {
		return
	}
	var current, size types.String
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("current_pool_name"), &current)...)
	response.Diagnostics.Append(request.State.GetAttribute(ctx, path.Root("size"), &size)...)
	if response.Diagnostics.HasError() {
		return
	}
	if current.IsNull() || current.IsUnknown() || current.ValueString() == "" {
		current = request.StateValue
	}
	response.RequiresReplace = current.ValueString() != managedReplacementPoolName(request.PlanValue.ValueString(), size.ValueString())
}
