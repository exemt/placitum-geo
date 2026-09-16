package pulse

import (
	"github.com/nats-io/nats.go"

	shared "github.com/exemt/placitum-shared/pulse"
)

type Work struct {
	Countries     int    `json:"countries,omitempty"`
	ASNs          int    `json:"asns,omitempty"`
	Skipped       int    `json:"skipped,omitempty"`
	Gen           uint64 `json:"gen,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	CountrySHA256 string `json:"country_sha256,omitempty"`
	ASNSHA256     string `json:"asn_sha256,omitempty"`
}

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
