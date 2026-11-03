package mcp_test

import (
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/release"
)

func TestSchemaDigestMatchesReleaseContract(t *testing.T) {
	want, err := release.ContractDigest()
	if err != nil {
		t.Fatalf("release contract digest: %v", err)
	}
	got := strings.TrimPrefix(mcp.SchemaDigest(), "sha256:")
	if got != want {
		t.Fatalf("mcp.SchemaDigest = %q, want release digest %q", got, want)
	}
}
