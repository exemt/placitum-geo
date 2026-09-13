# internal/config

Конфигурация процесса из переменных окружения. Пути к country и asn
проверяются при старте: опечатка — ошибка запуска, не пустой ответ на
первом запросе. Содержимое перечитывает store.

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `WAF_GEO_COUNTRY` | `./data/country` | Каталог стран, TSV или `GeoLite2-Country.mmdb` |
| `WAF_GEO_ASN` | `./data/asn` | Каталог ASN, TSV или `GeoLite2-ASN.mmdb` |
| `WAF_GEO_CONTROLLER_URL` | — (в образе `http://controller:8080`) | Откуда скачивать выгрузки, загруженные в панель (`GET /api/geo/files/<вид>`); пусто — не скачивать. Не `http(s)://host` — отказ старта |
| `WAF_GEO_FETCH_DIR` | `./data/fetched` (в образе `/var/lib/waf/geo`) | Скачанные копии; последняя целая поднимается при старте раньше `WAF_GEO_COUNTRY`/`WAF_GEO_ASN` |
| `WAF_GEO_HTTP` | `:8092` | HTTP listen; пусто — не слушать |
| `WAF_GEO_GRPC` | `:50051` | gRPC listen; пусто — не слушать |
| `WAF_GEO_RELOAD_EVERY` | `1s` | Как часто смотреть отпечаток файлов |
| `WAF_GEO_LOG` | `info` | Стартовый уровень журнала: словарь `error_log` nginx без `emerg` (`debug` … `alert`); живьём — «Журналы → Уровни журнала» (`docs/logger/logs.md`) |
| `WAF_NATS_URL` | `nats://127.0.0.1:4222` | Пульс `WAF_STATUS.service.geo.<id>` и документ `policy/geo`; пустая строка — не писать и выгрузки из панели не ждать |
| `WAF_SERVICE_NAME` | `geo` | Имя в кадре пульса |
| `WAF_HEARTBEAT_EVERY` | `4s` | Период пульса |
