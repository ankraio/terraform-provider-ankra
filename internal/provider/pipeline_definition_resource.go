// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

var (
	_ resource.Resource                   = (*pipelineDefinitionResource)(nil)
	_ resource.ResourceWithConfigure      = (*pipelineDefinitionResource)(nil)
	_ resource.ResourceWithImportState    = (*pipelineDefinitionResource)(nil)
	_ resource.ResourceWithValidateConfig = (*pipelineDefinitionResource)(nil)
)

type pipelineDefinitionResource struct {
	configuredClient
}

type pipelineDefinitionModel struct {
	ApplicationID types.String `tfsdk:"application_id"`
	RepositoryID  types.String `tfsdk:"repository_id"`
	SpecYAML      types.String `tfsdk:"spec_yaml"`
	SpecHash      types.String `tfsdk:"spec_hash"`
	Source        types.String `tfsdk:"source"`
	ProtectedHash types.String `tfsdk:"protected_hash"`
	Violations    types.List   `tfsdk:"violations"`
}

// NewPipelineDefinitionResource returns the ankra_pipeline_definition
// resource, the counterpart of `ankra pipeline definition put` and
// `ankra application pipeline definition put`.
func NewPipelineDefinitionResource() resource.Resource {
	return &pipelineDefinitionResource{}
}

func (definitionResource *pipelineDefinitionResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_pipeline_definition"
}

func pipelineSelectorAttributes(subject string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"application_id": schema.StringAttribute{
			MarkdownDescription: "Application whose linked repository " + subject + ". " + pipelineSelectorDescription + " The application must already be connected to Ankra Pipelines, which happens when its setup pull request is merged; until then the platform answers 404, so apply this in a second phase after a new `ankra_application`." +
				" Changing it replaces the resource.",
			Optional:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"repository_id": schema.StringAttribute{
			MarkdownDescription: "Connected repository that " + subject + ". " + pipelineSelectorDescription + " Changing it replaces the resource.",
			Optional:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
	}
}

func (definitionResource *pipelineDefinitionResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	attributes := pipelineSelectorAttributes("the definition belongs to")
	attributes["spec_yaml"] = schema.StringAttribute{
		MarkdownDescription: "Pipeline spec, in the `.ankra/pipeline.yaml` format, stored as the definition of record.",
		Required:            true,
	}
	attributes["spec_hash"] = schema.StringAttribute{
		MarkdownDescription: "Hash of the canonical spec the platform stored. A different definition of record, from a push of the committed file or a put outside Terraform, shows as a change to `spec_yaml`.",
		Computed:            true,
	}
	attributes["source"] = schema.StringAttribute{
		MarkdownDescription: "Where the definition of record came from.",
		Computed:            true,
	}
	attributes["protected_hash"] = schema.StringAttribute{
		MarkdownDescription: "Digest of the definition's protected sections, which an administrator approves with `ankra pipeline definitions approve`.",
		Computed:            true,
	}
	attributes["violations"] = schema.ListAttribute{
		MarkdownDescription: "Warnings the platform reported for the stored spec.",
		Computed:            true,
		ElementType:         types.StringType,
	}
	response.Schema = schema.Schema{
		MarkdownDescription: "The definition of record of a repository's Ankra pipeline, stored from a spec rather than read from the committed " +
			"`.ankra/pipeline.yaml`. The counterpart of `ankra pipeline definition put` and `ankra application pipeline definition put`. " +
			"\n\n**Use it only for a repository without a committed `.ankra/pipeline.yaml`.** Every push of a committed file records a " +
			"new definition of record, which Terraform reads as drift and the next apply puts back, so pushes and applies alternate " +
			"which pipeline runs. Commit the file or manage it here, not both.\n\n" +
			"**Protected sections still need an administrator.** A definition whose protected (authority) sections changed runs on the " +
			"default branch with the last approved authority until an administrator approves it with " +
			"`ankra pipeline definitions approve`; Terraform does not approve it.\n\n" +
			"The platform stores a canonical render of the spec, so drift is detected by `spec_hash`. The platform cannot delete " +
			"a definition: destroying this resource only removes it from Terraform state.",
		Attributes: attributes,
	}
}

func (definitionResource *pipelineDefinitionResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var model pipelineDefinitionModel
	response.Diagnostics.Append(request.Config.Get(ctx, &model)...)
	if response.Diagnostics.HasError() {
		return
	}
	pipelineValidateSelector(model.ApplicationID, model.RepositoryID, &response.Diagnostics)
}

func pipelineDefinitionToModel(definition *client.PipelineDefinition, model *pipelineDefinitionModel) {
	model.SpecHash = types.StringValue(definition.SpecHash)
	model.Source = types.StringValue(definition.Source)
	model.ProtectedHash = types.StringPointerValue(definition.ProtectedHash)
	violations := definition.Violations
	if violations == nil {
		violations = []string{}
	}
	model.Violations = listFromStrings(violations)
}

func (definitionResource *pipelineDefinitionResource) put(ctx context.Context, plan *pipelineDefinitionModel) error {
	definition, err := definitionResource.client.PutPipelineDefinition(ctx, pipelineSelectorFrom(plan.ApplicationID, plan.RepositoryID), plan.SpecYAML.ValueString())
	if err != nil {
		return err
	}
	pipelineDefinitionToModel(definition, plan)
	return nil
}

func (definitionResource *pipelineDefinitionResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if definitionResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan pipelineDefinitionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := definitionResource.put(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to store pipeline definition", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
	response.Diagnostics.Append(trackScope(ctx, definitionResource.client, response.Private)...)
}

func (definitionResource *pipelineDefinitionResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	apiClient := definitionResource.requireClient(&response.Diagnostics)
	if apiClient == nil {
		return
	}
	var state pipelineDefinitionModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	definition, err := apiClient.GetPipelineDefinition(ctx, pipelineSelectorFrom(state.ApplicationID, state.RepositoryID))
	if removeWhenNotFound(ctx, err, definitionResource.client, response) {
		return
	}
	if err != nil {
		response.Diagnostics.AddError("Unable to read pipeline definition", err.Error())
		return
	}
	if state.SpecHash.ValueString() != definition.SpecHash {
		state.SpecYAML = types.StringValue(definition.SpecYAML)
	}
	pipelineDefinitionToModel(definition, &state)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
	response.Diagnostics.Append(trackScope(ctx, definitionResource.client, response.Private)...)
}

func (definitionResource *pipelineDefinitionResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if definitionResource.requireClient(&response.Diagnostics) == nil {
		return
	}
	var plan pipelineDefinitionModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if err := definitionResource.put(ctx, &plan); err != nil {
		response.Diagnostics.AddError("Unable to store pipeline definition", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
}

// Delete only forgets the resource: the platform cannot delete a definition.
func (definitionResource *pipelineDefinitionResource) Delete(_ context.Context, _ resource.DeleteRequest, response *resource.DeleteResponse) {
	definitionResource.requireClient(&response.Diagnostics)
}

// ImportState imports by `application/<application_id>` or
// `repository/<repository_id>`. `spec_yaml` imports as the platform's
// canonical render, which can differ in layout from the configured spec.
func (definitionResource *pipelineDefinitionResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	parts, err := applicationSplitImportID(request.ID, "application|repository", "id")
	if err != nil {
		response.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	pipelineSelectorImport(ctx, parts[0], parts[1], response)
}
