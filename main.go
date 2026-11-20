package main

import (
	"context"
	"log"

	"github.com/e-volv/terraform-provider-evolve/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is set by goreleaser at release time.
var version = "dev"

func main() {
	if err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{Address: "registry.terraform.io/e-volv/evolve"}); err != nil {
		log.Fatal(err)
	}
}
