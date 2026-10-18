// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterBastionAllowedIPsResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterBastionAllowedIPsResource)(nil)
	_ resource.ResourceWithImportState = (*clusterBastionAllowedIPsResource)(nil)
)

// clusterBastionAllowedIPsMaximum is the platform's bound on the list: every
// entry becomes a firewall source on the bastion.
const clusterBastionAllowedIPsMaximum = 64

type clusterBastionAllowedIPsResource struct {
	configuredClient
}

// defaultBastionAllowedIPsClearTimeout bounds how long destroy waits for the
// operation that clears the list on the bastion.
const defaultBastionAllowedIPsClearTimeout = 10 * time.Minute

// bastionAllowedIPsPollInterval paces that wait; tests shorten it.
var bastionAllowedIPsPollInterval = 10 * time.Second

type clusterBastionAllowedIPsModel struct {
	ID          types.String   `tfsdk:"id"`
	ClusterID   types.String   `tfsdk:"cluster_id"`
	ClusterKind types.String   `tfsdk:"cluster_kind"`
	CIDRs       types.Set      `tfsdk:"cidrs"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

// NewClusterBastionAllowedIPsResource returns the
// ankra_cluster_bastion_allowed_ips resource.
func NewClusterBastionAllowedIPsResource() resource.Resource {
	return &clusterBastionAllowedIPsResource{}
}

func (allowedIPsResource *clusterBastionAllowedIPsResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_bastion_allowed_ips"
}

func (allowedIPsResource *clusterBastionAllowedIPsResource) Schema(ctx context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The IPv4 sources allowed to reach a cluster's bastion over SSH, on Hetzner, OVHcloud, UpCloud and " +
			"DigitalOcean clusters. The platform's own addresses are always allowed as well. Without this resource (or with an " +
			"empty `cidrs`), the bastion accepts SSH from any source, key-only. The platform applies a change to the bastion in the " +
			"background, so the apply returns once the list is stored. A list changed outside Terraform shows as drift. Destroying " +
			"the resource clears the list, which opens the bastion to every source again, and waits (up to the `delete` timeout, " +
			"default 10 minutes) until the platform has applied that to the bastion, so destroying the cluster in the same run does " +
			"not race it. Declare at most one per cluster. " +
			"Needs an Ankra platform from 2026-10-18 or later. AWS and Ankra Cloud clusters set their list with " +
			"`bastion_allowed_ips` on the cluster resource instead.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"cluster_kind": schema.StringAttribute{
				MarkdownDescription: "Kind of the cluster (`hetzner`, `ovh`, `upcloud` or `digitalocean`), resolved from the platform; it selects the API lane.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"cidrs": schema.SetAttribute{
				MarkdownDescription: fmt.Sprintf("IPv4 CIDRs allowed to reach the bastion over SSH, at most %d, written in canonical form "+
					"(`203.0.113.7/32` for one address, host bits zero). IPv6 and `0.0.0.0/0` are refused; an empty set clears the list.",
					clusterBastionAllowedIPsMaximum),
				Required:    true,
				ElementType: types.StringType,
				Validators:  []validator.Set{clusterBastionAllowedIPsValidator{}},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Delete: true}),
		},
	}
}

func (allowedIPsResource *clusterBastionAllowedIPsResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	apiClient := allowedIPsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterBastionAllowedIPsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := plan.ClusterID.ValueString()
	lane, ok := resolveBastionAllowlistLane(ctx, apiClient, clusterID, &response.Diagnostics)
	if !ok {
		return
	}
	cidrs := clusterBastionAllowedIPsFromSet(ctx, plan.CIDRs, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	// Read first: a platform that cannot read the list back could not
	// detect drift either, so refuse before writing anything.
	if _, err := apiClient.GetBastionAllowedIPs(ctx, lane, clusterID); err != nil {
		response.Diagnostics.AddError("Unable to read the bastion SSH allowlist", clusterBastionAllowedIPsErrorText(err, clusterID))
		return
	}
	if _, err := apiClient.SetBastionAllowedIPs(ctx, lane, clusterID, cidrs); err != nil {
		response.Diagnostics.AddError("Unable to set the bastion SSH allowlist", clusterBastionAllowedIPsErrorText(err, clusterID))
		return
	}
	plan.ID = plan.ClusterID
	plan.ClusterKind = types.StringValue(lane)
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (allowedIPsResource *clusterBastionAllowedIPsResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := allowedIPsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterBastionAllowedIPsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	lane := state.ClusterKind.ValueString()
	if lane == "" {
		cluster, err := apiClient.GetClusterByID(ctx, clusterID)
		if err != nil {
			response.Diagnostics.AddError("Unable to read cluster", err.Error())
			return
		}
		if cluster == nil {
			removeMissingCluster(ctx, apiClient, response)
			return
		}
		if !slices.Contains(client.BastionAllowlistLanes, cluster.Kind) {
			response.Diagnostics.AddError("Cluster has no bastion SSH allowlist", clusterBastionAllowlistLaneDetail(clusterID, cluster.Kind))
			return
		}
		lane = cluster.Kind
	}

	allowedIPs, err := apiClient.GetBastionAllowedIPs(ctx, lane, clusterID)
	if removeWhenNotFound(ctx, err, apiClient, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read the bastion SSH allowlist", clusterBastionAllowedIPsErrorText(err, clusterID))
		return
	}
	if !allowedIPs.Configured {
		// Never set on this bastion (for example a bastion rebuilt since):
		// the list this resource stored is gone, so plan to set it again.
		removeWhenTargetChanged(ctx, apiClient, response)
		return
	}
	state.ID = state.ClusterID
	state.ClusterKind = types.StringValue(lane)
	state.CIDRs = clusterBastionAllowedIPsSet(allowedIPs.BastionAllowedIPs)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, apiClient, response.Private)...)
}

func (allowedIPsResource *clusterBastionAllowedIPsResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	apiClient := allowedIPsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var plan, state clusterBastionAllowedIPsModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	cidrs := clusterBastionAllowedIPsFromSet(ctx, plan.CIDRs, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	if _, err := apiClient.SetBastionAllowedIPs(ctx, state.ClusterKind.ValueString(), state.ClusterID.ValueString(), cidrs); err != nil {
		response.Diagnostics.AddError("Unable to set the bastion SSH allowlist", clusterBastionAllowedIPsErrorText(err, state.ClusterID.ValueString()))
		return
	}
	plan.ID = state.ID
	plan.ClusterKind = state.ClusterKind
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete clears the list, which opens the bastion to every source again, and
// waits for the operation applying that to settle: a destroy of the whole
// configuration deletes the cluster right after, which would otherwise race
// the bastion update still in flight.
func (allowedIPsResource *clusterBastionAllowedIPsResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	apiClient := allowedIPsResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterBastionAllowedIPsModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	wait := resolveTimeout(ctx, state.Timeouts.Delete, defaultBastionAllowedIPsClearTimeout, &response.Diagnostics)
	if response.Diagnostics.HasError() {
		return
	}
	clusterID := state.ClusterID.ValueString()
	written, err := apiClient.SetBastionAllowedIPs(ctx, state.ClusterKind.ValueString(), clusterID, []string{})
	if err != nil {
		if deletedWhenNotFound(ctx, err, apiClient, request.Private, &response.Diagnostics) || response.Diagnostics.HasError() {
			return
		}
		response.Diagnostics.AddError("Unable to clear the bastion SSH allowlist", clusterBastionAllowedIPsErrorText(err, clusterID))
		return
	}
	if written.OperationID == nil || *written.OperationID == "" {
		return
	}
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	if err := awaitBastionAllowedIPsOperation(waitContext, apiClient, *written.OperationID); err != nil {
		response.Diagnostics.AddError("Clearing the bastion SSH allowlist did not finish",
			fmt.Sprintf("The list of cluster %s was cleared, but the operation applying that to the bastion %s. "+
				"Run the destroy again once it has settled; the list is already clear, so it then returns at once.", clusterID, err))
	}
}

// awaitBastionAllowedIPsOperation polls the operation that applies an
// allowlist write until it ends. An operation the platform no longer finds
// (404) has settled; one the token may not read (401, 403) cannot be
// followed, and is not waited for.
func awaitBastionAllowedIPsOperation(ctx context.Context, apiClient *client.Client, operationID string) error {
	for {
		execution, err := apiClient.GetExecution(ctx, operationID)
		switch {
		case client.IsNotFound(err), apiStatus(err) == http.StatusUnauthorized, apiStatus(err) == http.StatusForbidden:
			return nil
		case err == nil && execution.Status == client.ExecutionStatusSuccess:
			return nil
		case err == nil && client.IsExecutionFailed(execution.Status):
			detail := ""
			if execution.ErrorExcerpt != nil && *execution.ErrorExcerpt != "" {
				detail = ": " + *execution.ErrorExcerpt
			}
			return fmt.Errorf("(operation %s) ended %s%s", operationID, execution.Status, detail)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("(operation %s) was still running at the delete timeout", operationID)
		case <-time.After(bastionAllowedIPsPollInterval):
		}
	}
}

// ImportState imports by cluster id; the read resolves the cluster's kind.
func (allowedIPsResource *clusterBastionAllowedIPsResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

// resolveBastionAllowlistLane reads the cluster's kind, which names the API
// lane, and refuses a cluster whose bastion has no allowlist.
func resolveBastionAllowlistLane(ctx context.Context, apiClient *client.Client, clusterID string, diagnostics *diag.Diagnostics) (string, bool) {
	cluster, err := apiClient.GetClusterByID(ctx, clusterID)
	if err != nil {
		diagnostics.AddError("Unable to read cluster", err.Error())
		return "", false
	}
	if cluster == nil {
		diagnostics.AddAttributeError(path.Root("cluster_id"), "Cluster not found",
			fmt.Sprintf("No cluster %s is visible to this token.", clusterID))
		return "", false
	}
	if !slices.Contains(client.BastionAllowlistLanes, cluster.Kind) {
		diagnostics.AddAttributeError(path.Root("cluster_id"), "Cluster has no bastion SSH allowlist", clusterBastionAllowlistLaneDetail(clusterID, cluster.Kind))
		return "", false
	}
	return cluster.Kind, true
}

func clusterBastionAllowlistLaneDetail(clusterID, kind string) string {
	return fmt.Sprintf("Cluster %s is of kind %q. The bastion SSH allowlist exists on %s clusters; AWS and Ankra Cloud clusters "+
		"take `bastion_allowed_ips` on the cluster resource.", clusterID, kind, strings.Join(client.BastionAllowlistLanes, ", "))
}

// clusterBastionAllowedIPsErrorText explains the errors the allowlist routes
// answer: a platform without the read answers 405 on it.
func clusterBastionAllowedIPsErrorText(err error, clusterID string) string {
	var apiError *client.APIError
	if errors.As(err, &apiError) && apiError.StatusCode == http.StatusMethodNotAllowed {
		return err.Error() + "\n\nReading the bastion SSH allowlist needs an Ankra platform from 2026-10-18 or later."
	}
	return clusterSettingsErrorText(err, clusterID)
}

func clusterBastionAllowedIPsFromSet(ctx context.Context, cidrs types.Set, diagnostics *diag.Diagnostics) []string {
	values := []string{}
	if cidrs.IsNull() || cidrs.IsUnknown() {
		return values
	}
	diagnostics.Append(cidrs.ElementsAs(ctx, &values, false)...)
	slices.Sort(values)
	return values
}

func clusterBastionAllowedIPsSet(cidrs []string) types.Set {
	elements := make([]attr.Value, 0, len(cidrs))
	for _, cidr := range cidrs {
		elements = append(elements, types.StringValue(cidr))
	}
	return types.SetValueMust(types.StringType, elements)
}

// clusterBastionAllowedIPsCanonical answers the canonical form the platform
// stores an entry in, or a refusal naming why the platform refuses it.
func clusterBastionAllowedIPsCanonical(entry string) (string, string) {
	if strings.Contains(entry, ":") {
		return "", "is IPv6; the bastion is reached over IPv4, so list IPv4 addresses or CIDRs only"
	}
	candidate := strings.TrimSpace(entry)
	if !strings.Contains(candidate, "/") {
		address := net.ParseIP(candidate)
		if address == nil || address.To4() == nil {
			return "", "is not an IPv4 address or CIDR"
		}
		candidate += "/32"
	}
	address, network, err := net.ParseCIDR(candidate)
	if err != nil || address.To4() == nil {
		return "", "is not an IPv4 address or CIDR"
	}
	if prefixLength, _ := network.Mask.Size(); prefixLength == 0 {
		return "", "allows every source; use an empty set (or destroy the resource) to leave the bastion open"
	}
	return network.String(), ""
}

// clusterBastionAllowedIPsValidator refuses at plan time an entry the
// platform refuses, and one it would store in another spelling, which would
// otherwise plan a change on every run.
type clusterBastionAllowedIPsValidator struct{}

func (clusterBastionAllowedIPsValidator) Description(_ context.Context) string {
	return fmt.Sprintf("at most %d IPv4 CIDRs in canonical form, without 0.0.0.0/0", clusterBastionAllowedIPsMaximum)
}

func (allowedIPsValidator clusterBastionAllowedIPsValidator) MarkdownDescription(ctx context.Context) string {
	return allowedIPsValidator.Description(ctx)
}

func (clusterBastionAllowedIPsValidator) ValidateSet(_ context.Context, request validator.SetRequest, response *validator.SetResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	elements := request.ConfigValue.Elements()
	if len(elements) > clusterBastionAllowedIPsMaximum {
		response.Diagnostics.AddAttributeError(request.Path, "Too many CIDRs",
			fmt.Sprintf("The bastion SSH allowlist holds at most %d entries; this configuration sets %d.", clusterBastionAllowedIPsMaximum, len(elements)))
	}
	for _, element := range elements {
		value, isString := element.(types.String)
		if !isString || value.IsNull() || value.IsUnknown() {
			continue
		}
		entry := value.ValueString()
		canonical, refusal := clusterBastionAllowedIPsCanonical(entry)
		switch {
		case refusal != "":
			response.Diagnostics.AddAttributeError(request.Path, "Invalid CIDR", fmt.Sprintf("Entry %q %s.", entry, refusal))
		case canonical != entry:
			response.Diagnostics.AddAttributeError(request.Path, "CIDR not in canonical form",
				fmt.Sprintf("Entry %q is stored by the platform as %q; write it that way so the plan stays empty.", entry, canonical))
		}
	}
}
