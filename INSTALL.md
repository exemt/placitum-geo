# Installation

English · [Русский](INSTALL.ru.md)

Install the coder when rules use countries or autonomous systems, or when the operator needs the
address card in the panel. Usually `placitum-core` installs it.

## What it needs

| Component | Required | Why |
| --- | --- | --- |
| Catalog of countries and AS | for meaningful answers | a file uploaded in the panel or two catalog paths; without them the coder answers "unknown" |
| NATS | no | log, live log level, presence frame and the `policy/geo` document; without the bus a file uploaded in the panel never reaches the coder |
| Controller | no | asks for address cards over HTTP; the coder downloads uploaded files from it |

The coder has no database. It needs as much memory as the expanded catalog: a million prefixes take
about 70 MiB, and the installation from core gives it 4 GB with `GOMEMLIMIT=3GiB`. During a swap
two sets of tables of one kind are in memory.

## Settings

| Variable | Default | Purpose |
| --- | --- | --- |
| `WAF_GEO_COUNTRY` | `./data/country`; `/app/data/country` in the image | country catalog: a directory, TSV or `GeoLite2-Country.mmdb`; the path must exist |
| `WAF_GEO_ASN` | `./data/asn`; `/app/data/asn` in the image | AS catalog; the path must exist |
| `WAF_GEO_CONTROLLER_URL` | empty; `http://controller:8080` in the image | where to download files uploaded in the panel; empty means catalog paths only |
| `WAF_GEO_FETCH_DIR` | `./data/fetched`; `/var/lib/waf/geo` in the image | downloaded copies; the latest complete one wins over the catalog paths at start |
| `WAF_GEO_HTTP` | `:8092` | HTTP: address card and `/healthz`; empty turns it off |
| `WAF_GEO_GRPC` | `:50051` | gRPC `geo.v1.Geo/Lookup` for inspectors; empty turns it off |
| `WAF_GEO_RELOAD_EVERY` | `1s` | how often to check the catalog files |
| `WAF_NATS_URL` | `nats://127.0.0.1:4222`; `nats://nats:4222` in the image | bus; an empty value turns it off |
| `WAF_GEO_LOG` | `info` | log level |
| `WAF_SERVICE_NAME` | `geo` | name in the presence frame |
| `WAF_HEARTBEAT_EVERY` | `4s` | presence frame interval |
| `GOMEMLIMIT` | — | soft heap limit: about three quarters of the container limit |

The image does not create the catalog directories: mount them. Geo data is licensed separately and
does not ship in the image. The copies directory `/var/lib/waf/geo` exists in the image and belongs
to the process; a volume on it keeps the copy when the container is recreated.

## Catalog

The operator uploads the GeoLite2 export in the panel. The controller checks the file against its
own catalog, stores the file and names it in the `policy/geo` document in the `WAF_DESIRED` KV. For
every revision of the document the coder:

1. downloads `GET <controller>/api/geo/files/<kind>` to a temporary file;
2. checks size and hash against the document; a mismatch means the controller already has a newer
   file and a new document is on its way;
3. builds the tables of that kind next to the current ones, while requests keep using the old ones;
4. swaps the snapshot at once and only then removes the previous copy.

If a step fails (the controller is unreachable, the hash does not match), the coder stays on what it
has and retries with a backoff from one second to half a minute. Copies in `WAF_GEO_FETCH_DIR`
survive restarts.

The directories under `/app/data` are the fallback: the coder uses them until the first upload and
where there is no controller. Empty directories are a working state: country and AS rules reject
with a machine-readable code, everything else works.

## Docker Compose

```yaml
services:
  geo:
    image: placitum/geo
    environment:
      WAF_NATS_URL: nats://nats:4222
      WAF_GEO_CONTROLLER_URL: http://controller:8080
      WAF_GEO_LOG: info
      GOMEMLIMIT: 3GiB
    volumes:
      - ./geo-data:/app/data:ro
      - geo-copies:/var/lib/waf/geo
    mem_limit: 4g

volumes:
  geo-copies:
```

No published ports are needed: inspectors and the controller reach the coder inside the network.

## Checking

```sh
curl -fsS http://127.0.0.1:8092/healthz
curl -fsS 'http://127.0.0.1:8092/lookup?addr=8.8.8.8'
```

An empty answer for a real address means an empty catalog, not a failure: the log shows how many
records were loaded.

The presence frame shows which file the coder uses: `work.country_sha256` and `work.asn_sha256`
(empty means the catalog paths) and `conf` with the `policy/geo` revision and outcome (`ok`,
`fetching`, `fetch_failed`). The panel compares these hashes with the uploaded file.

## Pitfalls

- **Catalog paths must exist.** Empty directories are fine, missing ones stop the start.
- **Panel uploads travel only over the bus.** Without NATS the `policy/geo` document is not read and
  the coder stays on the catalog paths.
- **Inspectors wait synchronously.** The default budget of half a second is headroom for a bad
  minute of the network, not thinking time: the answer itself takes a fraction of a millisecond.
- **DNS inside the installation.** A slow resolve of the name `geo` eats the same budget.
