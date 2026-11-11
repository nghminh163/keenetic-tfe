// terraform-provider-keenetic is an OpenTofu/Terraform provider for Keenetic routers (RCI API).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/nghminh163/terraform-provider-keenetic/internal/provider"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/nghminh163/keenetic",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
