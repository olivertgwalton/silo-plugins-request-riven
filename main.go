// Command silo-plugins-requests-riven implements the Silo request_router.v1
// and scan_source.v1 capabilities backed directly by a riven-rs instance's
// GraphQL API.
package main

import (
	_ "embed"

	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"

	"github.com/olivertgwalton/silo-plugins-request-riven/internal/router"
	"github.com/olivertgwalton/silo-plugins-request-riven/internal/scansource"
)

var version string

//go:embed manifest.json
var manifestJSON []byte

func main() {
	runtime.ServeManifest(manifestJSON, version, runtime.CapabilityServers{
		RequestRouter: router.New(),
		ScanSource:    scansource.New(),
	})
}
