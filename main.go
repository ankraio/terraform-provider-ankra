package main

import (
	   "github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	   "ankra.io/terraform-provider-ankra/internal/provider"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: provider.Provider,
	})
}
