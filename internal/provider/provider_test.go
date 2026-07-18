// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// testAccProtoV6ProviderFactories is used by acceptance tests to spin up the
// provider under a protocol v6 server.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"ankra": providerserver.NewProtocol6WithError(New("test")()),
}

func TestProviderSchemaIsValid(t *testing.T) {
	t.Parallel()

	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("failed to build provider server: %v", err)
	}

	response, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema returned an error: %v", err)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("schema diagnostic error: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
	}

	if _, ok := response.ResourceSchemas["ankra_cluster"]; !ok {
		t.Error("expected ankra_cluster resource to be registered")
	}
	if _, ok := response.DataSourceSchemas["ankra_clusters"]; !ok {
		t.Error("expected ankra_clusters data source to be registered")
	}
}
