package main

import (
	"testing"

	publicmanifest "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
)

func TestEmbeddedManifestLoads(t *testing.T) {
	m, err := publicmanifest.LoadWithChecksum(manifestJSON, version)
	if err != nil {
		t.Fatalf("LoadWithChecksum: %v", err)
	}
	if m.GetPluginId() != "silo.requests.riven" {
		t.Fatalf("plugin_id: want silo.requests.riven got %q", m.GetPluginId())
	}
	caps := m.GetCapabilities()
	if len(caps) != 2 {
		t.Fatalf("want 2 capabilities, got %d", len(caps))
	}
	if caps[0].GetType() != "request_router.v1" || caps[0].GetId() != "riven" {
		t.Fatalf("capability[0]: want request_router.v1/riven got %q/%q", caps[0].GetType(), caps[0].GetId())
	}
	if caps[1].GetType() != "scan_source.v1" || caps[1].GetId() != "riven" {
		t.Fatalf("capability[1]: want scan_source.v1/riven got %q/%q", caps[1].GetType(), caps[1].GetId())
	}
}
