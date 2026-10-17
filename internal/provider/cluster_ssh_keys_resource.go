// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                = (*clusterSSHKeysResource)(nil)
	_ resource.ResourceWithConfigure   = (*clusterSSHKeysResource)(nil)
	_ resource.ResourceWithImportState = (*clusterSSHKeysResource)(nil)
)

// clusterSSHKeysKinds are the cluster kinds the platform manages SSH keys
// for; each serves the route under its own provider prefix.
var clusterSSHKeysKinds = map[string]bool{
	"hetzner": true, "ovh": true, "upcloud": true, "digitalocean": true, "scaleway": true,
	"ankracloud": true, "aws": true, "proxmox": true, "morpheus": true,
}

type clusterSSHKeysResource struct {
	configuredClient
}

type clusterSSHKeysModel struct {
	ID                  types.String `tfsdk:"id"`
	ClusterID           types.String `tfsdk:"cluster_id"`
	SSHKeyCredentialIDs types.Set    `tfsdk:"ssh_key_credential_ids"`
}

// NewClusterSSHKeysResource returns the ankra_cluster_ssh_keys resource, the
// counterpart of `ankra cluster ssh-keys set`.
func NewClusterSSHKeysResource() resource.Resource {
	return &clusterSSHKeysResource{}
}

func (keysResource *clusterSSHKeysResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_ssh_keys"
}

func (keysResource *clusterSSHKeysResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The SSH keys installed on the nodes of a cluster Ankra provisioned (Hetzner, OVHcloud, UpCloud, DigitalOcean, " +
			"Scaleway, Ankra Cloud, AWS, Proxmox VE, HPE Morpheus). The counterpart of `ankra cluster ssh-keys set`.\n\n" +
			"There is one set per cluster, so declare one of these per cluster. Destroying the resource only removes it from " +
			"state: the cluster keeps the keys it has, because no key set is a safe default to restore.",
		Attributes: map[string]schema.Attribute{
			"id":         clusterSettingsIDAttribute("The cluster id."),
			"cluster_id": clusterSettingsClusterIDAttribute(),
			"ssh_key_credential_ids": schema.SetAttribute{
				MarkdownDescription: "Identifiers of the organisation SSH key credentials to install (`ankra credentials ssh-key`). An empty set removes every key.",
				Required:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (keysResource *clusterSSHKeysResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	keysResource.write(ctx, request.Plan.Get, &response.State, &response.Diagnostics)
}

func (keysResource *clusterSSHKeysResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	keysResource.write(ctx, request.Plan.Get, &response.State, &response.Diagnostics)
}

func (keysResource *clusterSSHKeysResource) write(ctx context.Context, getPlan func(context.Context, any) diag.Diagnostics, state interface {
	Set(context.Context, any) diag.Diagnostics
}, diagnostics *diag.Diagnostics) {
	apiClient := keysResource.requireClient(diagnostics)
	if apiClient == nil {
		return
	}
	var plan clusterSSHKeysModel
	diagnostics.Append(getPlan(ctx, &plan)...)
	if diagnostics.HasError() {
		return
	}
	kind, err := clusterSSHKeysKind(ctx, apiClient, plan.ClusterID.ValueString())
	if err != nil {
		diagnostics.AddError("Unable to set cluster SSH keys", err.Error())
		return
	}
	credentialIDs := clusterSettingsStringsFromSet(ctx, plan.SSHKeyCredentialIDs, diagnostics)
	if diagnostics.HasError() {
		return
	}
	if _, err := apiClient.SetClusterSSHKeys(ctx, kind, plan.ClusterID.ValueString(), credentialIDs); err != nil {
		diagnostics.AddError("Unable to set cluster SSH keys", err.Error())
		return
	}
	plan.ID = plan.ClusterID
	diagnostics.Append(state.Set(ctx, &plan)...)
}

func (keysResource *clusterSSHKeysResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := keysResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state clusterSSHKeysModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	cluster, err := apiClient.GetClusterByID(ctx, state.ClusterID.ValueString())
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster SSH keys", err.Error())
		return
	}
	if cluster == nil {
		response.Diagnostics.AddError("Cluster not in scope", fmt.Sprintf("cluster %s is not in the cluster listing of the provider's organisation and project scope. "+
			"Set the provider's project_id (or organisation_id) to the scope that holds the cluster; if the cluster was deleted, "+
			"remove this resource with `terraform state rm`.", state.ClusterID.ValueString()))
		return
	}
	if !clusterSSHKeysKinds[cluster.Kind] {
		response.Diagnostics.AddError("Unable to read cluster SSH keys", clusterSSHKeysKindError(cluster).Error())
		return
	}
	keys, err := apiClient.GetClusterSSHKeys(ctx, cluster.Kind, cluster.ID)
	if clusterSettingsReadNotFound(ctx, err, state.ClusterID.ValueString(), response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read cluster SSH keys", err.Error())
		return
	}
	state.ID = types.StringValue(cluster.ID)
	state.SSHKeyCredentialIDs = clusterSettingsSetFromStrings(keys.SSHKeyCredentialIDs)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Delete leaves the cluster's keys in place and only forgets them.
func (keysResource *clusterSSHKeysResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	response.Diagnostics.AddWarning("Cluster SSH keys left in place",
		"The cluster keeps its SSH keys; ankra_cluster_ssh_keys was only removed from state.")
}

// ImportState imports by cluster id.
func (keysResource *clusterSSHKeysResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	clusterSettingsImportClusterID(ctx, request, response)
}

func clusterSSHKeysKind(ctx context.Context, apiClient *client.Client, clusterID string) (string, error) {
	cluster, err := apiClient.GetClusterByID(ctx, clusterID)
	if err != nil {
		return "", err
	}
	if cluster == nil {
		return "", fmt.Errorf("cluster %s is not in the cluster listing of the provider's organisation and project scope; set the provider's project_id", clusterID)
	}
	if !clusterSSHKeysKinds[cluster.Kind] {
		return "", clusterSSHKeysKindError(cluster)
	}
	return cluster.Kind, nil
}

func clusterSSHKeysKindError(cluster *client.Cluster) error {
	return fmt.Errorf("cluster %s is of kind %q, which has no SSH key management; only clusters Ankra provisions do", cluster.ID, cluster.Kind)
}

func clusterSettingsStringsFromSet(ctx context.Context, set types.Set, diagnostics *diag.Diagnostics) []string {
	values := []string{}
	if set.IsNull() || set.IsUnknown() {
		return values
	}
	var elements []types.String
	diagnostics.Append(set.ElementsAs(ctx, &elements, false)...)
	for _, element := range elements {
		if !element.IsNull() && !element.IsUnknown() {
			values = append(values, element.ValueString())
		}
	}
	return values
}

func clusterSettingsSetFromStrings(values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.SetValueMust(types.StringType, elements)
}
