// Command silo-plugins-requests-riven implements the Silo request_router.v1
// capability backed directly by a riven-rs instance's GraphQL request API.
package main

import (
	_ "embed"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"

	"github.com/olivertgwalton/silo-plugins-request-riven/internal/router"
)

var version string

//go:embed manifest.json
var manifestJSON []byte

func main() {
	runtime.ServeManifest(manifestJSON, version, runtime.CapabilityServers{
		RequestRouter: router.New(),
	})
}
