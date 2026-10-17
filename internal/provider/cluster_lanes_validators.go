// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// laneOneOfValidator refuses a string outside the set the platform accepts,
// so a value the API would reject with 422 fails the plan instead of the
// apply.
type laneOneOfValidator struct {
	allowed []string
}

// laneOneOf returns a validator accepting only the given values.
func laneOneOf(allowed ...string) validator.String {
	return laneOneOfValidator{allowed: allowed}
}

func (oneOf laneOneOfValidator) Description(_ context.Context) string {
	return "value must be one of: " + strings.Join(oneOf.allowed, ", ")
}

func (oneOf laneOneOfValidator) MarkdownDescription(ctx context.Context) string {
	return oneOf.Description(ctx)
}

func (oneOf laneOneOfValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if slices.Contains(oneOf.allowed, request.ConfigValue.ValueString()) {
		return
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
		fmt.Sprintf("%q is not accepted; expected one of: %s.", request.ConfigValue.ValueString(), strings.Join(oneOf.allowed, ", ")))
}

// laneAtLeastValidator refuses an integer below a bound.
type laneAtLeastValidator struct {
	minimum int64
}

// laneAtLeastOne returns a validator accepting positive integers.
func laneAtLeastOne() validator.Int64 {
	return laneAtLeastValidator{minimum: 1}
}

func (atLeast laneAtLeastValidator) Description(_ context.Context) string {
	return fmt.Sprintf("value must be at least %d", atLeast.minimum)
}

func (atLeast laneAtLeastValidator) MarkdownDescription(ctx context.Context) string {
	return atLeast.Description(ctx)
}

func (atLeast laneAtLeastValidator) ValidateInt64(_ context.Context, request validator.Int64Request, response *validator.Int64Response) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if request.ConfigValue.ValueInt64() >= atLeast.minimum {
		return
	}
	response.Diagnostics.AddAttributeError(request.Path, "Invalid value",
		fmt.Sprintf("%d is below the minimum of %d.", request.ConfigValue.ValueInt64(), atLeast.minimum))
}

// laneNameValidator refuses a name the platform cannot route: node group and
// pool names travel as a URL path segment.
type laneNameValidator struct{}

func (laneNameValidator) Description(_ context.Context) string {
	return "name must be non-empty and must not contain '/'"
}

func (nameValidator laneNameValidator) MarkdownDescription(ctx context.Context) string {
	return nameValidator.Description(ctx)
}

func (laneNameValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	name := request.ConfigValue.ValueString()
	if strings.TrimSpace(name) == "" || strings.Contains(name, "/") {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid name",
			fmt.Sprintf("%q cannot be used: the name must be non-empty and must not contain '/'.", name))
	}
}

// Value sets the platform validates against, shared by the lane resources.
var (
	laneDistributions   = []string{"kubeadm", "k3s"}
	laneCNIs            = []string{"flannel", "calico", "cilium"}
	laneEtcdTopologies  = []string{"stacked", "external"}
	laneRetentionPolicy = []string{"delete", "retain"}
	laneTaintEffects    = []string{"NoSchedule", "PreferNoSchedule", "NoExecute"}
)
