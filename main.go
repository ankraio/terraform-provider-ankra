// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"ankra.io/terraform-provider-ankra/internal/provider"
)

// version is set at build time by goreleaser via -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	options := providerserver.ServeOpts{
		Address:         "registry.terraform.io/ankraio/ankra",
		Debug:           debug,
		ProtocolVersion: 6,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), options); err != nil {
		log.Fatal(err.Error())
	}
}
