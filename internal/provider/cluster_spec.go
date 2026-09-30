// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// This file maps the ankra_cluster stacks blocks onto the import route's
// ResourceSpecification. The shapes follow what the platform parses today
// (cluster go/internal/importedapi/specparse.go), which drifted from what
// provider 0.1.x sent in three places:
//
//   - parents are {name, kind} objects, not bare strings;
//   - an addon names its Helm registry with registry_name AND registry_url;
//   - job_configuration is an object of integer timeouts, not a string.
//
// The Terraform schema keeps the 0.1.x attribute types so existing state and
// configuration keep decoding, and the conversion below bridges the gap.

// maxRegistryNameLength matches the platform's limit on Helm registry names.
const maxRegistryNameLength = 63

// memberKinds indexes every manifest and addon name declared across all
// stacks, so a bare parent name can be resolved to its kind.
type memberKinds map[string]map[string]struct{}

func indexMembers(stacks []stackModel) memberKinds {
	index := memberKinds{}
	add := func(name, kind string) {
		if name == "" {
			return
		}
		if index[name] == nil {
			index[name] = map[string]struct{}{}
		}
		index[name][kind] = struct{}{}
	}
	for _, stack := range stacks {
		for _, manifest := range stack.Manifests {
			add(manifest.Name.ValueString(), client.ParentKindManifest)
		}
		for _, addon := range stack.Addons {
			add(addon.Name.ValueString(), client.ParentKindAddon)
		}
	}
	return index
}

// stacksToAPI converts the configured stacks into the import payload. Every
// problem is reported against the attribute that caused it; the returned
// stacks must not be sent when the diagnostics carry an error.
func stacksToAPI(stacks []stackModel) ([]client.Stack, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	members := indexMembers(stacks)
	result := make([]client.Stack, 0, len(stacks))
	for stackIndex, stack := range stacks {
		stackPath := path.Root("stacks").AtListIndex(stackIndex)
		apiStack := client.Stack{
			Name:        stack.Name.ValueString(),
			Description: stack.Description.ValueString(),
		}
		for manifestIndex, manifest := range stack.Manifests {
			memberPath := stackPath.AtName("manifests").AtListIndex(manifestIndex)
			apiStack.Manifests = append(apiStack.Manifests, client.Manifest{
				Name:           manifest.Name.ValueString(),
				Namespace:      manifest.Namespace.ValueString(),
				ManifestBase64: manifest.ManifestBase64.ValueString(),
				Parents:        parentsToAPI(manifest.Parents, members, memberPath.AtName("parents"), &diagnostics),
				FromFile:       manifest.FromFile.ValueString(),
			})
		}
		for addonIndex, addon := range stack.Addons {
			memberPath := stackPath.AtName("addons").AtListIndex(addonIndex)
			apiStack.Addons = append(apiStack.Addons, addonToAPI(addon, members, memberPath, &diagnostics))
		}
		result = append(result, apiStack)
	}
	return result, diagnostics
}

func addonToAPI(addon addonModel, members memberKinds, memberPath path.Path, diagnostics *diag.Diagnostics) client.Addon {
	registryURL, registryName := addonRegistry(addon, memberPath, diagnostics)
	apiAddon := client.Addon{
		Name:                   addon.Name.ValueString(),
		ChartName:              addon.ChartName.ValueString(),
		ChartVersion:           addon.ChartVersion.ValueString(),
		RegistryName:           registryName,
		RegistryURL:            registryURL,
		RegistryCredentialName: addon.RegistryCredentialName.ValueString(),
		Namespace:              addon.Namespace.ValueString(),
		Parents:                parentsToAPI(addon.Parents, members, memberPath.AtName("parents"), diagnostics),
	}
	if values := addon.Configuration.ValueString(); strings.TrimSpace(values) != "" {
		apiAddon.Configuration = &client.AddonConfiguration{ValuesBase64: helmValuesBase64(values)}
	}
	jobConfiguration, jobError := jobConfigurationToAPI(addon.JobConfiguration.ValueString())
	if jobError != nil {
		diagnostics.AddAttributeError(memberPath.AtName("job_configuration"), "Invalid job_configuration",
			"job_configuration must be a JSON object of integer timeouts in seconds, for example "+
				`jsonencode({ create_job_timeout = 600, update_job_timeout = 600 }). `+
				"Accepted keys: create_job_timeout, read_job_timeout, update_job_timeout, delete_job_timeout. "+
				jobError.Error())
	}
	apiAddon.JobConfiguration = jobConfiguration
	return apiAddon
}

// addonRegistry resolves the registry URL and name an addon is sent with.
// registry_url wins; the deprecated repository_url is its alias. Without a
// registry_name the name is derived from the URL: the platform accepts any
// name when the URL matches a registry already connected to the organisation.
func addonRegistry(addon addonModel, memberPath path.Path, diagnostics *diag.Diagnostics) (string, string) {
	registryURL := strings.TrimSpace(addon.RegistryURL.ValueString())
	legacyURL := strings.TrimSpace(addon.RepositoryURL.ValueString())
	switch {
	case registryURL != "" && legacyURL != "" && registryURL != legacyURL:
		diagnostics.AddAttributeError(memberPath.AtName("repository_url"), "Conflicting registry URLs",
			"repository_url is a deprecated alias of registry_url; set only registry_url.")
	case registryURL == "":
		registryURL = legacyURL
	}
	if registryURL == "" {
		diagnostics.AddAttributeError(memberPath.AtName("registry_url"), "Missing registry_url",
			"Every addon needs the URL of the Helm registry that serves its chart (registry_url).")
	}
	registryName := strings.TrimSpace(addon.RegistryName.ValueString())
	if registryName == "" {
		registryName = registryNameFromURL(registryURL)
	}
	return registryURL, registryName
}

// registryNameFromURL derives a stable registry name from its URL, e.g.
// https://kubernetes.github.io/ingress-nginx -> kubernetes-github-io-ingress-nginx.
func registryNameFromURL(registryURL string) string {
	trimmed := registryURL
	if parsed, err := url.Parse(registryURL); err == nil && parsed.Host != "" {
		trimmed = parsed.Host + parsed.Path
	}
	var builder strings.Builder
	lastDash := true
	for _, character := range strings.ToLower(trimmed) {
		isAlphanumeric := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
		if isAlphanumeric {
			builder.WriteRune(character)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(builder.String(), "-")
	if len(name) > maxRegistryNameLength {
		name = strings.TrimRight(name[:maxRegistryNameLength], "-")
	}
	if name == "" {
		return "helm-registry"
	}
	return name
}

// helmValuesBase64 accepts Helm values either as base64 (what 0.1.x
// documented) or as plain YAML, and returns them base64-encoded. YAML values
// always carry a ':' or whitespace, neither of which is a base64 character,
// so the two cannot be confused.
func helmValuesBase64(values string) string {
	candidate := strings.TrimSpace(values)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		if decoded, err := encoding.DecodeString(candidate); err == nil {
			return base64.StdEncoding.EncodeToString(decoded)
		}
	}
	return base64.StdEncoding.EncodeToString([]byte(values))
}

// jobConfigurationToAPI parses the job_configuration string attribute, which
// must hold a JSON object of integer timeouts.
func jobConfigurationToAPI(raw string) (*client.JobConfiguration, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	var configuration client.JobConfiguration
	if err := decoder.Decode(&configuration); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("unexpected content after the JSON object")
	}
	return &configuration, nil
}

// parentsToAPI converts parent references. Each reference is either
// "<kind>:<name>" (kind is manifest or addon) or a bare name, whose kind is
// looked up among the manifests and addons declared in this resource. A null
// list stays nil so the platform keeps the stored edges; an empty list is
// sent as [] and clears them.
func parentsToAPI(values []types.String, members memberKinds, parentsPath path.Path, diagnostics *diag.Diagnostics) *[]client.Parent {
	if values == nil {
		return nil
	}
	parents := make([]client.Parent, 0, len(values))
	for index, value := range values {
		parent, problem := resolveParent(value.ValueString(), members)
		if problem != "" {
			diagnostics.AddAttributeError(parentsPath.AtListIndex(index), "Invalid parent", problem)
			continue
		}
		parents = append(parents, parent)
	}
	return &parents
}

func resolveParent(reference string, members memberKinds) (client.Parent, string) {
	reference = strings.TrimSpace(reference)
	for _, kind := range []string{client.ParentKindManifest, client.ParentKindAddon} {
		if name, found := strings.CutPrefix(reference, kind+":"); found {
			name = strings.TrimSpace(name)
			if name == "" {
				return client.Parent{}, fmt.Sprintf("Parent %q names no %s.", reference, kind)
			}
			return client.Parent{Name: name, Kind: kind}, ""
		}
	}
	if reference == "" {
		return client.Parent{}, "A parent must name a manifest or addon."
	}
	kinds := members[reference]
	switch {
	case len(kinds) == 0:
		return client.Parent{}, fmt.Sprintf(
			"Parent %q is not a manifest or addon declared in this resource's stacks. "+
				"Declare it, or reference a member defined elsewhere explicitly as "+
				"\"manifest:%s\" or \"addon:%s\".", reference, reference, reference)
	case len(kinds) > 1:
		return client.Parent{}, fmt.Sprintf(
			"Parent %q is both a manifest and an addon; write \"manifest:%s\" or \"addon:%s\".",
			reference, reference, reference)
	}
	for kind := range kinds {
		return client.Parent{Name: reference, Kind: kind}, ""
	}
	return client.Parent{}, ""
}

// stacksFullyKnown reports whether every value the conversion reads is known,
// so configuration validation can run the conversion without reporting
// unknown values as missing.
func stacksFullyKnown(stacks []stackModel) bool {
	known := func(values ...types.String) bool {
		for _, value := range values {
			if value.IsUnknown() {
				return false
			}
		}
		return true
	}
	for _, stack := range stacks {
		for _, manifest := range stack.Manifests {
			if !known(manifest.Name) || !known(manifest.Parents...) {
				return false
			}
		}
		for _, addon := range stack.Addons {
			if !known(addon.Name, addon.RegistryURL, addon.RepositoryURL, addon.RegistryName,
				addon.Configuration, addon.JobConfiguration) || !known(addon.Parents...) {
				return false
			}
		}
	}
	return true
}
