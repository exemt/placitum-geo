package grpcapi

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exemt/placitum-geo/internal/lookup"
	"github.com/exemt/placitum-geo/internal/store"
	geopb "github.com/exemt/placitum-geo/proto"
)

type Server struct {
	geopb.UnimplementedGeoServer
	store *store.Store
}

func New(st *store.Store) *Server {
	return &Server{store: st}
}

// Lookup -- тот же ответ, что у HTTP, поле в поле: один разбор на оба входа,
// расходиться им незачем.
func (s *Server) Lookup(_ context.Context, req *geopb.LookupRequest) (*geopb.LookupResponse, error) {
	addr := ""
	expand := false

	if req != nil {
		addr = req.GetAddr()
		expand = req.GetExpandAsn()
	}

	got, err := lookup.DoWith(s.store.Current(), addr, lookup.Options{ExpandASN: expand})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	out := &geopb.LookupResponse{
		Gen:       got.Gen,
		Countries: make([]*geopb.Country, 0, len(got.Countries)),
		Asns:      make([]*geopb.ASN, 0, len(got.ASNs)),
	}

	for _, c := range got.Countries {
		out.Countries = append(out.Countries, &geopb.Country{Code: c.Code, Name: c.Name})
	}

	for _, a := range got.ASNs {
		row := &geopb.ASN{
			Asn:       a.ASN,
			Name:      a.Name,
			Prefix:    a.Prefix,
			Effective: a.Effective,
			Prefixes:  a.Prefixes,
		}

		if a.Range != nil {
			row.Range = &geopb.Range{Start: a.Range.Start, End: a.Range.End}
		}

		out.Asns = append(out.Asns, row)
	}

	return out, nil
}
