/*
 * Присутствие на WAF_STATUS — та же шина, что у агента и логгера.
 * Не lookup и не healthcheck контейнера. Контроллер слушает WAF_STATUS.>
 * и ставит degraded по тишине.
 *
 * Шапка кадра общая (pulse.Frame из placitum-shared); своё здесь — work:
 * размер и поколение загруженного каталога.
 */

package pulse

import (
	"github.com/nats-io/nats.go"

	shared "github.com/exemt/placitum-shared/pulse"
)

type Work struct {
	Countries   int    `json:"countries,omitempty"`
	ASNs        int    `json:"asns,omitempty"`
	Skipped     int    `json:"skipped,omitempty"`
	Gen         uint64 `json:"gen,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

type Message struct {
	shared.Frame
	Work Work `json:"work"`
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
