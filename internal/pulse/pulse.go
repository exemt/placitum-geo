/*
 * Присутствие на WAF_STATUS — та же шина, что у агента и логгера.
 * Не lookup и не healthcheck контейнера. Контроллер слушает WAF_STATUS.>
 * и ставит degraded по тишине.
 *
 * Шапка кадра общая (pulse.Frame из placitum-shared); своё здесь — work:
 * размер и поколение загруженного каталога, и conf: какой документ
 * policy/geo кодер применил (internal/fetch).
 */

package pulse

import (
	"github.com/nats-io/nats.go"

	shared "github.com/exemt/placitum-shared/pulse"
)

/*
 * Work -- что кодер держит в памяти. CountrySHA256 и ASNSHA256 -- хеш копии
 * выгрузки из панели, по которой он отвечает; пусто -- каталог из окружения.
 * По ним панель видит, переключился ли кодер на загруженный файл.
 */
type Work struct {
	Countries     int    `json:"countries,omitempty"`
	ASNs          int    `json:"asns,omitempty"`
	Skipped       int    `json:"skipped,omitempty"`
	Gen           uint64 `json:"gen,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	CountrySHA256 string `json:"country_sha256,omitempty"`
	ASNSHA256     string `json:"asn_sha256,omitempty"`
}

// Conf -- документ policy/geo, за который кодер брался последним: ревизия,
// хеш и исход. Форма та же, что `conf` у агента haproxy.
type Conf struct {
	Rev    int    `json:"rev"`
	SHA256 string `json:"sha256"`
	Apply  string `json:"apply"`
}

type Message struct {
	shared.Frame
	Work Work  `json:"work"`
	Conf *Conf `json:"conf,omitempty"`
}

func NewID() string {
	return shared.NewID()
}

func Subject(name, id string) string {
	return shared.ServiceSubject(name, id)
}

func Build(id, name string, ready bool, work Work) Message {
	return Message{
		Frame: shared.NewFrame("service", id, name, ready, nil),
		Work:  work,
	}
}

func Publish(nc *nats.Conn, msg Message) error {
	return shared.PublishFrame(nc, Subject(msg.Name, msg.ID), msg)
}
