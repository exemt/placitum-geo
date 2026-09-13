package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	t.Setenv("WAF_GEO_COUNTRY", filepath.Join(root, "country"))
	t.Setenv("WAF_GEO_ASN", filepath.Join(root, "asn"))
	t.Setenv("WAF_GEO_HTTP", ":0")
	t.Setenv("WAF_GEO_GRPC", "")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if c.HTTP != ":0" || c.GRPC != "" {
		t.Fatalf("listen http=%q grpc=%q", c.HTTP, c.GRPC)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("WAF_GEO_COUNTRY", filepath.Join(os.TempDir(), "no-such-geo-country"))
	t.Setenv("WAF_GEO_ASN", filepath.Join("..", "..", "testdata", "asn"))

	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadControllerURL(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	t.Setenv("WAF_GEO_COUNTRY", filepath.Join(root, "country"))
	t.Setenv("WAF_GEO_ASN", filepath.Join(root, "asn"))
	t.Setenv("WAF_GEO_CONTROLLER_URL", "http://controller:8080/")
	t.Setenv("WAF_GEO_FETCH_DIR", "fetched")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if c.ControllerURL != "http://controller:8080" {
		t.Fatalf("controller url = %q", c.ControllerURL)
	}

	if !filepath.IsAbs(c.FetchDir) {
		t.Fatalf("fetch dir = %q", c.FetchDir)
	}

	/* Опечатка в адресе -- отказ старта, а не кодер на старой выгрузке. */
	t.Setenv("WAF_GEO_CONTROLLER_URL", "controller:8080")

	if _, err := Load(); err == nil {
		t.Fatal("url without scheme accepted")
	}
}
