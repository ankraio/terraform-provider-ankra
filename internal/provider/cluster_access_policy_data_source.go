// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*clusterAccessPolicyDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*clusterAccessPolicyDataSource)(nil)
)

type clusterAccessPolicyDataSource struct {
	configuredDataSourceClient
}

type clusterAccessPolicyDataSourceModel struct {
	ID                    types.String `tfsdk:"id"`
	OrganisationID        types.String `tfsdk:"organisation_id"`
	OrganisationName      types.String `tfsdk:"organisation_name"`
	IsConfigured          types.Bool   `tfsdk:"is_configured"`
	CreatorGrantRole      types.String `tfsdk:"creator_grant_role"`
	MaxGrantRole          types.String `tfsdk:"max_grant_role"`
	ElevatedMaxTTLSeconds types.Int64  `tfsdk:"elevated_max_ttl_seconds"`
	RequireReasonFromRole types.String `tfsdk:"require_reason_from_role"`
}

// NewClusterAccessPolicyDataSource returns the ankra_cluster_access_policy
// data source, the counterpart of `ankra org access-policy get`.
func NewClusterAccessPolicyDataSource() datasource.DataSource {
	return &clusterAccessPolicyDataSource{}
}

func (policySource *clusterAccessPolicyDataSource) Metadata(_ context.Context, request datasource.MetadataRequest, response *datasource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_cluster_access_policy"
}

func (policySource *clusterAccessPolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, response *datasource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The organisation's Kubernetes access policy: the limits every kubectl access grant through the cluster " +
			"gateway is held to, an administrator's included. The counterpart of `ankra org access-policy get`. Reading it needs " +
			"`kube_access.manage`, `kube_access.policy` or `kube_access.elevate`, and the cluster gateway must be enabled.\n\n" +
			"This is read-only on purpose. Changing the policy needs `kube_access.policy`, a human-only permission that no token " +
			"carries unless it names it, and the platform mints no API token that can name it; so a Terraform write could never " +
			"succeed. Propose a change with `ankra org access-policy set` and have an organisation owner or admin apply it, signed " +
			"in. Use this data source to check the policy (for example with a `check` block or a precondition on " +
			"`ankra_cluster_access_grant`) or to size grant lifetimes inside it.",
		Attributes: map[string]schema.Attribute{
			"id":                computedStringAttribute("Always `cluster_access_policy`."),
			"organisation_id":   computedStringAttribute("Organisation the policy belongs to."),
			"organisation_name": computedStringAttribute("Name of that organisation."),
			"is_configured":     computedBoolAttribute("Whether the organisation has set a policy. While it is `false` the platform defaults below apply: the creator of a cluster gets `cluster-admin` on it and anyone can be given any access."),
			"creator_grant_role": computedStringAttribute("What whoever creates or imports a cluster gets on it: `none`, `view`, `edit`, `admin` or " +
				"`cluster-admin`."),
			"max_grant_role": computedStringAttribute("The ceiling: the most access anyone can be given, `view`, `edit`, `admin` or `cluster-admin`."),
			"elevated_max_ttl_seconds": schema.Int64Attribute{
				MarkdownDescription: "How long a grant above `view` may last, in seconds; null when the policy sets no limit (break-glass " +
					"elevation is then still bounded to 4 hours).",
				Computed: true,
			},
			"require_reason_from_role": computedStringAttribute("The lowest role whose grants need a reason; null when only break-glass " +
				"elevation needs one."),
		},
	}
}

func (policySource *clusterAccessPolicyDataSource) Read(ctx context.Context, _ datasource.ReadRequest, response *datasource.ReadResponse) {
	apiClient := policySource.requireOrganisationClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	read, readError := apiClient.GetClusterAccessPolicy(ctx)
	if readError != nil {
		response.Diagnostics.AddError("Unable to read the cluster access policy", readError.Error())
		return
	}
	state := clusterAccessPolicyDataSourceModel{
		ID:                    types.StringValue("cluster_access_policy"),
		OrganisationID:        types.StringValue(read.OrganisationID),
		OrganisationName:      types.StringPointerValue(read.OrganisationName),
		IsConfigured:          types.BoolValue(read.Policy.IsConfigured),
		CreatorGrantRole:      types.StringValue(read.Policy.CreatorGrantRole),
		MaxGrantRole:          types.StringValue(read.Policy.MaxGrantRole),
		ElevatedMaxTTLSeconds: types.Int64PointerValue(read.Policy.ElevatedMaxTTLSeconds),
		RequireReasonFromRole: types.StringPointerValue(read.Policy.RequireReasonFromRole),
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}
