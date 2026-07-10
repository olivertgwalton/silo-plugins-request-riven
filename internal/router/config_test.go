package router

import (
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestConnectionFromRouter(t *testing.T) {
	c := connectionFromRouter(&pluginv1.RouterConnection{
		Id:      "c1",
		BaseUrl: "http://localhost:8080",
		ApiKey:  "secret",
	})
	if c.ID != "c1" || c.BaseURL != "http://localhost:8080" || c.APIKey != "secret" {
		t.Fatalf("bad connection: %+v", c)
	}
}
