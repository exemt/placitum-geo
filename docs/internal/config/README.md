# internal/config

Конфигурация процесса из переменных окружения. Пути к country и asn
проверяются при старте: опечатка — ошибка запуска, не пустой ответ на
первом запросе. Содержимое перечитывает store.

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `WAF_GEO_COUNTRY` | `./data/country` | Каталог стран, TSV или `GeoLite2-Country.mmdb` |
| `WAF_GEO_ASN` | `./data/asn` | Каталог ASN, TSV или `GeoLite2-ASN.mmdb` |
| `WAF_GEO_HTTP` | `:8092` | HTTP listen; пусто — не слушать |
| `WAF_GEO_GRPC` | `:50051` | gRPC listen; пусто — не слушать |
| `WAF_GEO_RELOAD_EVERY` | `1s` | Как часто смотреть отпечаток файлов |
| `WAF_GEO_LOG` | `info` | Стартовый уровень журнала: словарь `error_log` nginx без `emerg` (`debug` … `alert`); живьём — «Журналы → Уровни журнала» (`docs/logger/logs.md`) |
| `WAF_NATS_URL` | `nats://127.0.0.1:4222` | Пульс `WAF_STATUS.service.geo.<id>`; пустая строка — не писать |
| `WAF_SERVICE_NAME` | `geo` | Имя в кадре пульса |
| `WAF_HEARTBEAT_EVERY` | `4s` | Период пульса |
