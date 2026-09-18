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

The catalog is neither a database nor a network service: files on disk that the process keeps in
memory and rereads by itself. An empty catalog is a working state: the network directory answers "unknown", and
rules that need it reject explicitly instead of staying silent.

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

## Good to know

- **An answer costs a fraction of a millisecond**, but inspectors wait for it synchronously within
  the message budget: a miss means a rejected rule line, not a slower request.
- **The catalog updates on the fly.** Upload a file in the panel or put files into the directory:
  the process picks them up without a restart.
- **No state of its own.** Run as many replicas as you like, each with its own catalog.

## License

[Placitum License Agreement](LICENSE.md). A Russian translation is in [LICENSE.ru.md](LICENSE.ru.md);
the English text is the legally binding one.
