// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// defaultCreateTimeout bounds a provisioning wait when the practitioner does
// not set one. Building a control plane and its nodes is a minutes-long job.
const defaultCreateTimeout = 60 * time.Minute

// defaultDeleteTimeout bounds a deprovisioning wait.
const defaultDeleteTimeout = 30 * time.Minute

func requiredReplaceString(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func defaultedReplaceString(description, defaultValue string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Optional:            true,
		Computed:            true,
		Default:             stringdefault.StaticString(defaultValue),
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func defaultedReplaceInt64(description string, defaultValue int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: description,
		Optional:            true,
		Computed:            true,
		Default:             int64default.StaticInt64(defaultValue),
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
	}
}

func defaultedReplaceBool(description string, defaultValue bool) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: description,
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(defaultValue),
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
	}
}

func optionalReplaceString(description string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: description,
		Optional:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func optionalReplaceStringList(description string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: description,
		Optional:            true,
		ElementType:         types.StringType,
		PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
	}
}

// forceDestroyAttribute exposes the deprovisioning force flag. It affects a
// future destroy rather than the running cluster, so it deliberately carries
// no RequiresReplace.
func forceDestroyAttribute(lane string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Send `force=true` when deprovisioning, which makes the platform tear down the " +
			"cluster's " + lane + " resources without its usual guards. Defaults to `true` for backwards " +
			"compatibility; set it to `false` to take the guarded delete path. Changing this only affects a " +
			"future destroy, so it never replaces the cluster.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(true),
	}
}

// waitForReadyAttribute controls whether an apply blocks until the platform
// reports the cluster running.
func waitForReadyAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Wait for the platform to report the cluster `running` before the resource is " +
			"considered created. Defaults to `true`; set it to `false` to return as soon as the platform " +
			"accepts the request. Anything that consumes the cluster - a kubeconfig, a `kubernetes` provider, " +
			"a dependent module - needs the wait, because provisioning is asynchronous.",
		Optional: true,
		Computed: true,
		Default:  booldefault.StaticBool(true),
	}
}

// identityAttributes are the computed attributes every cluster resource
// carries. state and kind are refreshed from the platform on every read, which
// is what makes a cluster that changed underneath Terraform visible at all.
func identityAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			MarkdownDescription: "Identifier of the cluster (mirrors `cluster_id`).",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"cluster_id": schema.StringAttribute{
			MarkdownDescription: "Identifier assigned to the cluster by the Ankra platform.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"state": schema.StringAttribute{
			MarkdownDescription: "Lifecycle state the platform reports for the cluster, refreshed on every " +
				"read (for example `creating`, `running`, `stopped`).",
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"kind": schema.StringAttribute{
			MarkdownDescription: "Cluster kind the platform reports (for example `hetzner`, `imported`).",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
	}
}

// commonProvisionedBlocks are the blocks every cloud lane shares. The timeouts
// block uses the conventional block syntax practitioners expect:
//
//	timeouts {
//	  create = "90m"
//	}
func commonProvisionedBlocks(ctx context.Context) map[string]schema.Block {
	return map[string]schema.Block{
		"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Delete: true}),
	}
}

// commonProvisionedAttributes are the arguments every cloud lane shares. A
// lane adds its own capacity and placement attributes on top, keeping each
// cloud's own vocabulary.
func commonProvisionedAttributes(_ context.Context) map[string]schema.Attribute {
	attributes := identityAttributes()
	attributes["name"] = requiredReplaceString("Name of the cluster.")
	attributes["credential_id"] = requiredReplaceString("Cloud API credential id used to provision the cluster.")
	attributes["distribution"] = defaultedReplaceString("Kubernetes distribution: `kubeadm` (default, vanilla upstream Kubernetes) or `k3s`.", "kubeadm")
	attributes["cni"] = defaultedReplaceString("Container network interface. Defaults to `cilium`, the only CNI kubeadm clusters run; set `flannel` or `calico` for k3s clusters.", "cilium")
	attributes["kubernetes_version"] = optionalReplaceString(
		"Kubernetes version. Defaults to the platform's stable version when unset.")
	attributes["control_plane_count"] = defaultedReplaceInt64("Number of control-plane nodes.", 1)
	attributes["worker_count"] = defaultedReplaceInt64("Number of worker nodes.", 1)
	attributes["external_cloud_provider"] = defaultedReplaceBool(
		"Install the cloud controller manager and CSI driver.", true)
	attributes["include_networking"] = defaultedReplaceBool(
		"Install Traefik and cert-manager for ingress.", true)
	attributes["etcd_topology"] = defaultedReplaceString(
		"etcd topology for kubeadm clusters: `stacked` or `external`.", "stacked")
	attributes["etcd_node_count"] = defaultedReplaceInt64(
		"Number of dedicated etcd nodes when `etcd_topology` is `external`.", 3)
	attributes["gitops_branch"] = defaultedReplaceString(
		"GitOps branch to commit the generated stack to.", "master")
	attributes["gitops_credential_name"] = optionalReplaceString(
		"GitOps GitHub credential name; commits the generated stack to Git when set with `gitops_repository`.")
	attributes["gitops_repository"] = optionalReplaceString(
		"GitOps repository (`owner/name`) to commit the generated stack to.")
	attributes["description"] = optionalReplaceString("Description of the cluster.")
	attributes["wait_for_ready"] = waitForReadyAttribute()
	return attributes
}

// optionalStringPointer maps an unset or empty attribute to a nil pointer, so
// a nullable contract field is omitted rather than sent empty.
func optionalStringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return nil
	}
	result := value.ValueString()
	return &result
}

// stringListToAPI maps a Terraform string list to a plain slice.
func stringListToAPI(ctx context.Context, value types.List, diagnostics *diag.Diagnostics) []string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	var values []string
	diagnostics.Append(value.ElementsAs(ctx, &values, false)...)
	return values
}

// timeoutFunc is the shape of a timeouts.Value accessor such as
// Timeouts.Create or Timeouts.Delete.
type timeoutFunc = func(context.Context, time.Duration) (time.Duration, diag.Diagnostics)

// resolveTimeout reads a configured timeout, falling back to the default.
func resolveTimeout(ctx context.Context, configured timeoutFunc, fallback time.Duration, diagnostics *diag.Diagnostics) time.Duration {
	resolved, timeoutDiagnostics := configured(ctx, fallback)
	diagnostics.Append(timeoutDiagnostics...)
	if resolved <= 0 {
		return fallback
	}
	return resolved
}

// awaitProvisioned blocks until the platform reports the cluster running, and
// returns the refreshed row so the caller can record state and kind.
func awaitProvisioned(ctx context.Context, apiClient *client.Client, clusterID string, wait time.Duration, diagnostics *diag.Diagnostics) *client.Cluster {
	waitContext, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	cluster, waitError := apiClient.WaitForProvisionedCluster(waitContext, clusterID, 0)
	if waitError != nil {
		diagnostics.AddError("Cluster did not become ready", waitError.Error())
		return nil
	}
	return cluster
}

// applyClusterIdentity records the platform-reported state and kind, leaving
// the fields null when the platform did not report them.
func applyClusterIdentity(state *types.String, kind *types.String, cluster *client.Cluster) {
	if cluster == nil {
		return
	}
	if cluster.State != "" {
		*state = types.StringValue(cluster.State)
	} else {
		*state = types.StringNull()
	}
	if cluster.Kind != "" {
		*kind = types.StringValue(cluster.Kind)
	} else {
		*kind = types.StringNull()
	}
}

// settleProvisioned resolves the cluster row after a create: it waits for the
// platform to report the cluster running when the practitioner asked for it,
// and otherwise reads the row once so state and kind are still recorded.
func settleProvisioned(ctx context.Context, apiClient *client.Client, clusterID string, waitForReady types.Bool, createTimeout timeoutFunc, diagnostics *diag.Diagnostics) *client.Cluster {
	if !waitForReady.IsNull() && !waitForReady.IsUnknown() && !waitForReady.ValueBool() {
		cluster, readError := apiClient.GetClusterByID(ctx, clusterID)
		if readError != nil {
			diagnostics.AddError("Unable to read cluster", readError.Error())
			return nil
		}
		if cluster == nil {
			return &client.Cluster{ID: clusterID}
		}
		return cluster
	}

	wait := resolveTimeout(ctx, createTimeout, defaultCreateTimeout, diagnostics)
	if diagnostics.HasError() {
		return nil
	}
	return awaitProvisioned(ctx, apiClient, clusterID, wait, diagnostics)
}
