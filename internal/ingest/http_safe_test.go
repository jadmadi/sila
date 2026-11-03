package ingest

import (
	"net/url"
	"strings"
	"testing"
)

func TestValidatePublicURLBlocksPrivateAddresses(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1", "http://[::1]", "http://169.254.1.1", "http://10.0.0.1"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validatePublicURL(u); err == nil {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}
	u, _ := url.Parse("https://example.com/docs")
	if err := validatePublicURL(u); err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
}

func TestReadLimitedRejectsOversizedResponse(t *testing.T) {
	if _, err := readLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected size limit error")
	}
}
