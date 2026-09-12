package grpcapi

import (
	"context"
	"strings"
	"testing"

	geopb "github.com/exemt/placitum-geo/proto"
)

func TestLookupExpand(t *testing.T) {
	c := testClient(t)

	got, err := c.Lookup(context.Background(), &geopb.LookupRequest{Addr: "1.1.1.1", ExpandAsn: true})
	if err != nil {
		t.Fatal(err)
	}

	if got.GetGen() == 0 {
		t.Fatal("gen missing")
	}

	if len(got.GetAsns()) != 1 {
		t.Fatalf("asns: %+v", got.GetAsns())
	}

	row := got.GetAsns()[0]
	if row.GetAsn() != 13335 || !row.GetEffective() || row.GetPrefix() != "1.1.1.0/24" {
		t.Fatalf("effective row: %+v", row)
	}

	if row.GetRange() == nil || row.GetRange().GetStart() != "1.1.1.0" || row.GetRange().GetEnd() != "1.1.1.255" {
		t.Fatalf("range: %+v", row.GetRange())
	}

	if strings.Join(row.GetPrefixes(), " ") != "1.0.0.0/24 1.1.1.0/24 2606:4700:4700::/48" {
		t.Fatalf("composition: %v", row.GetPrefixes())
	}
}

func TestLookupNoExpand(t *testing.T) {
	c := testClient(t)

	got, err := c.Lookup(context.Background(), &geopb.LookupRequest{Addr: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.GetAsns()) != 1 || len(got.GetAsns()[0].GetPrefixes()) != 0 {
		t.Fatalf("plain lookup carries no composition: %+v", got.GetAsns())
	}

	if got.GetAsns()[0].GetPrefix() != "8.8.8.0/24" || !got.GetAsns()[0].GetEffective() {
		t.Fatalf("prefix and effective: %+v", got.GetAsns()[0])
	}
}
