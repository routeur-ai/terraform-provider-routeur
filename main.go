package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/routeur-ai/terraform-provider-routeur/internal/provider"
)

// registryAddress is the provider's Terraform Registry address.
const registryAddress = "registry.terraform.io/routeur-ai/routeur"

// version is set by goreleaser at build time.
var version = "dev"

//go:generate go tool tfplugindocs generate --provider-name routeur

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: registryAddress,
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
