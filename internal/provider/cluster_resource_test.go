// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// legacyStacks is a 0.1.x-shaped configuration: repository_url, bare-string
// parents, and job_configuration as a string.
func legacyStacks() []stackModel {
	return []stackModel{
		{
			Name:        types.StringValue("platform"),
			Description: types.StringValue("core stack"),
			Manifests: []manifestModel{
				{
					Name:           types.StringValue("namespace"),
					Namespace:      types.StringValue("test-ns"),
					ManifestBase64: types.StringValue("YmFzZTY0"),
					FromFile:       types.StringNull(),
				},
			},
			Addons: []addonModel{
				{
					Name:             types.StringValue("ingress"),
					ChartName:        types.StringValue("traefik"),
					ChartVersion:     types.StringValue("37.0.0"),
					RepositoryURL:    types.StringValue("https://traefik.github.io/charts"),
					Namespace:        types.StringValue("ingress"),
					Configuration:    types.StringValue("deployment:\n  replicas: 2\n"),
					Parents:          []types.String{types.StringValue("namespace")},
					JobConfiguration: types.StringValue(`{"create_job_timeout":600}`),
				},
			},
		},
	}
}

// TestStacksToAPIMatchesPlatformShapes pins the wire shapes the platform's
// spec parser requires today.
func TestStacksToAPIMatchesPlatformShapes(t *testing.T) {
	t.Parallel()

	stacks, diagnostics := stacksToAPI(legacyStacks())
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	encoded, err := json.Marshal(stacks)
	if err != nil {
		t.Fatal(err)
	}
	// Decode into the wire shapes the platform parses, so a string where an
	// object is required fails the unmarshal rather than passing unnoticed.
	var decoded []struct {
		Manifests []map[string]json.RawMessage `json:"manifests"`
		Addons    []struct {
			RegistryURL      string                     `json:"registry_url"`
			RegistryName     string                     `json:"registry_name"`
			RepositoryURL    *string                    `json:"repository_url"`
			Parents          []client.Parent            `json:"parents"`
			JobConfiguration map[string]json.RawMessage `json:"job_configuration"`
			Configuration    client.AddonConfiguration  `json:"configuration"`
		} `json:"addons"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("payload does not match the platform shapes: %v\n%s", err, encoded)
	}
	addon := decoded[0].Addons[0]

	if addon.RegistryURL != "https://traefik.github.io/charts" {
		t.Errorf("registry_url = %q", addon.RegistryURL)
	}
	if addon.RegistryName != "traefik-github-io-charts" {
		t.Errorf("registry_name = %q", addon.RegistryName)
	}
	if addon.RepositoryURL != nil {
		t.Error("repository_url must not be sent; the platform reads registry_url")
	}
	if len(addon.Parents) != 1 || addon.Parents[0] != (client.Parent{Name: "namespace", Kind: "manifest"}) {
		t.Errorf("parents = %+v, want [{namespace manifest}]", addon.Parents)
	}
	if string(addon.JobConfiguration["create_job_timeout"]) != "600" {
		t.Errorf("job_configuration = %v, want an object with create_job_timeout 600", addon.JobConfiguration)
	}
	if _, present := addon.JobConfiguration["read_job_timeout"]; present {
		t.Error("unset job timeouts must be omitted so the platform defaults apply")
	}
	values, _ := base64.StdEncoding.DecodeString(addon.Configuration.ValuesBase64)
	if string(values) != "deployment:\n  replicas: 2\n" {
		t.Errorf("values = %q", values)
	}
	if _, present := decoded[0].Manifests[0]["parents"]; present {
		t.Error("unset parents must be omitted so the platform keeps the stored edges")
	}
}

func TestParentsToAPI(t *testing.T) {
	t.Parallel()

	stacks := []stackModel{{
		Name: types.StringValue("a"),
		Manifests: []manifestModel{
			{Name: types.StringValue("ns")},
			{Name: types.StringValue("both")},
		},
		Addons: []addonModel{{Name: types.StringValue("db")}, {Name: types.StringValue("both")}},
	}, {
		Name:   types.StringValue("b"),
		Addons: []addonModel{{Name: types.StringValue("cache")}},
	}}
	members := indexMembers(stacks)

	cases := []struct {
		reference string
		want      client.Parent
		problem   string
	}{
		{reference: "ns", want: client.Parent{Name: "ns", Kind: "manifest"}},
		{reference: "db", want: client.Parent{Name: "db", Kind: "addon"}},
		{reference: "cache", want: client.Parent{Name: "cache", Kind: "addon"}},
		{reference: "manifest:elsewhere", want: client.Parent{Name: "elsewhere", Kind: "manifest"}},
		{reference: "addon:both", want: client.Parent{Name: "both", Kind: "addon"}},
		{reference: "both", problem: "both a manifest and an addon"},
		{reference: "missing", problem: "not a manifest or addon declared"},
		{reference: "addon:", problem: "names no addon"},
		{reference: "", problem: "must name"},
	}
	for _, testCase := range cases {
		parent, problem := resolveParent(testCase.reference, members)
		if testCase.problem != "" {
			if !strings.Contains(problem, testCase.problem) {
				t.Errorf("%q: problem = %q, want it to mention %q", testCase.reference, problem, testCase.problem)
			}
			continue
		}
		if problem != "" || parent != testCase.want {
			t.Errorf("%q: got %+v (%q), want %+v", testCase.reference, parent, problem, testCase.want)
		}
	}
}

func TestParentsNullVersusEmpty(t *testing.T) {
	t.Parallel()

	stacks := legacyStacks()
	stacks[0].Addons[0].Parents = []types.String{}
	result, diagnostics := stacksToAPI(stacks)
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	encoded, _ := json.Marshal(result[0].Addons[0])
	if !strings.Contains(string(encoded), `"parents":[]`) {
		t.Errorf("an explicit empty parents list must be sent as [] to clear edges: %s", encoded)
	}
}

func TestAddonRegistryResolution(t *testing.T) {
	t.Parallel()

	base := legacyStacks()[0].Addons[0]
	base.Parents = nil

	explicit := base
	explicit.RepositoryURL = types.StringNull()
	explicit.RegistryURL = types.StringValue("oci://ghcr.io/traefik/helm")
	explicit.RegistryName = types.StringValue("traefik")
	result, diagnostics := stacksToAPI([]stackModel{{Name: types.StringValue("s"), Addons: []addonModel{explicit}}})
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	if addon := result[0].Addons[0]; addon.RegistryURL != "oci://ghcr.io/traefik/helm" || addon.RegistryName != "traefik" {
		t.Errorf("explicit registry lost: %+v", addon)
	}

	conflicting := explicit
	conflicting.RepositoryURL = types.StringValue("https://other.example.com")
	if _, diagnostics := stacksToAPI([]stackModel{{Name: types.StringValue("s"), Addons: []addonModel{conflicting}}}); !diagnostics.HasError() {
		t.Error("conflicting registry_url and repository_url must be refused")
	}

	missing := base
	missing.RepositoryURL = types.StringNull()
	if _, diagnostics := stacksToAPI([]stackModel{{Name: types.StringValue("s"), Addons: []addonModel{missing}}}); !diagnostics.HasError() {
		t.Error("an addon without a registry URL must be refused")
	}
}

func TestRegistryNameFromURL(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"https://kubernetes.github.io/ingress-nginx": "kubernetes-github-io-ingress-nginx",
		"https://charts.jetstack.io/":                "charts-jetstack-io",
		"oci://registry-1.docker.io/bitnamicharts":   "registry-1-docker-io-bitnamicharts",
		"":                                   "helm-registry",
		"https://" + strings.Repeat("a", 80): strings.Repeat("a", 63),
	}
	for input, want := range cases {
		if got := registryNameFromURL(input); got != want {
			t.Errorf("registryNameFromURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHelmValuesBase64(t *testing.T) {
	t.Parallel()

	encoded := base64.StdEncoding.EncodeToString([]byte("replicaCount: 2\n"))
	if got := helmValuesBase64(encoded); got != encoded {
		t.Errorf("base64 input must pass through, got %q", got)
	}
	if got := helmValuesBase64("replicaCount: 2\n"); got != encoded {
		t.Errorf("YAML input must be encoded, got %q", got)
	}
}

func TestJobConfigurationToAPI(t *testing.T) {
	t.Parallel()

	if configuration, err := jobConfigurationToAPI(""); err != nil || configuration != nil {
		t.Errorf("empty = %+v, %v", configuration, err)
	}
	configuration, err := jobConfigurationToAPI(`{"create_job_timeout": 600, "delete_job_timeout": 120}`)
	if err != nil || *configuration.CreateJobTimeout != 600 || *configuration.DeleteJobTimeout != 120 ||
		configuration.ReadJobTimeout != nil {
		t.Errorf("parsed = %+v, %v", configuration, err)
	}
	for _, invalid := range []string{`600`, `{"timeout": 1}`, `{"create_job_timeout": "600"}`, `{} {}`} {
		if _, err := jobConfigurationToAPI(invalid); err == nil {
			t.Errorf("%q must be refused", invalid)
		}
	}
}

// TestAnkraTokenDoesNotForceReplacement pins that rotating the deprecated
// per-resource token is an in-place update (0.1.x marked it ForceNew, so a
// rotation planned destroy + create of the cluster).
func TestAnkraTokenDoesNotForceReplacement(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	NewClusterResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["ankra_token"].(schema.StringAttribute)
	if !ok {
		t.Fatal("ankra_token is not a string attribute")
	}
	if len(attribute.PlanModifiers) != 0 {
		t.Errorf("ankra_token must carry no plan modifiers (RequiresReplace), got %d", len(attribute.PlanModifiers))
	}
}
