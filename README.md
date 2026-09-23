# Placitum geo

English · [Русский](README.ru.md)

Placitum network directory: country and autonomous system number by client address.

It answers two clients: inspectors over gRPC, when a rule writes a subnet or a whole AS to a
dataset instead of an address, and the panel over HTTP, for the address card. It stays out of the
module's hot path.

```
inspector ──gRPC :50051──►  geo  ◄──HTTP :8092── controller (address card)
                             │ ▲
                             │ └── copy of the file uploaded in the panel: KV policy/geo → GET /api/geo/files/<kind>
                             └── catalog: country prefixes and AS announcements
```

The catalog is a set of files on disk; the process keeps them in memory and rereads them by itself.
An empty catalog is a working state: the network directory answers "unknown", and rules that need it
reject explicitly.

The operator uploads the MaxMind export in the panel. The network directory learns about it from a KV document,
downloads a copy from the controller, builds new tables next to the current ones and swaps them in;
until the swap it keeps answering from the previous tables.

## Build and run

```sh
docker build -t placitum/geo .
docker run --rm -p 8092:8092 -p 50051:50051 \
  -v /path/country:/app/data/country:ro \
  -v /path/asn:/app/data/asn:ro \
  placitum/geo
```

What it needs, settings and where the catalog comes from are in [INSTALL.md](INSTALL.md).

## API

gRPC `geo.v1.Geo/Lookup` (`proto/geo.proto`) and the same data over HTTP:

| Request | What |
| --- | --- |
| `GET /lookup?addr=8.8.8.8` | an address or a CIDR; `expand=asn` adds the full AS composition |
| `POST /lookup/batch` with `{"addrs": [...]}` | up to 500 addresses in one request; a bad address marks only its own item |
| `GET /healthz` | liveness |

```json
{
  "gen": 17,
  "countries": [{"code": "us", "name": "United States"}],
  "asns": [
    {"asn": 15169, "name": "GOOGLE", "prefix": "8.8.8.0/24",
     "effective": true, "range": {"start": "8.8.8.0", "end": "8.8.8.255"}},
    {"asn": 3356, "name": "LEVEL3", "prefix": "8.0.0.0/9"}
  ]
}
```

For an address the answer lists every announcement covering it, from the narrowest to the widest;
the effective one comes first and carries `range`. For a range every AS appears once. An unknown
address gives empty lists, not an error; bad input gives `400` or `InvalidArgument`. `gen` changes
when the catalog changes, and clients drop their caches.

## Catalog formats

A path can point to a directory, a `.tsv` file or a `.mmdb` file; the format follows what is on disk.

| Source | Country | ASN |
| --- | --- | --- |
| Directory | `<code>.txt` or `<code>/ranges.txt` | `<number>.txt` or `<number>/ranges.txt` |
| TSV | controller export: `code type address name` | the same, `code` is the AS number |
| MMDB | `GeoLite2-Country.mmdb` | `GeoLite2-ASN.mmdb` |

A text file has one prefix or address per line; `#` starts a comment, and `# name: United States`
sets the label. Country codes are ISO 3166-1 alpha-2 in lower case.

## Running it

An answer costs a fraction of a millisecond, and inspectors wait for it synchronously within the
message budget, so a miss rejects the rule line and does not slow the request down. The catalog
updates on the fly: upload a file in the panel or put files into the directory, and the process picks
them up without a restart. The process keeps no state of its own, so run as many replicas as you
like, each with its own catalog.

## License

[Apache License 2.0](LICENSE); the attribution notice is in [NOTICE](NOTICE). This repository is
part of the Placitum open core. The inspectors are licensed separately: each inspector repository
carries the Placitum License Agreement. Versions up to 1.0.1 were released under the Placitum
License Agreement 1.1.
