/*
 * Точка входа geo/asn кодера.
 *
 * Таблицы собираются до listen: ошибка в каталоге обязана обнаружиться
 * при старте, а не на первом запросе. Дальше store сам перечитывает диск.
 */

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/exemt/placitum-geo/internal/config"
	"github.com/exemt/placitum-geo/internal/grpcapi"
	"github.com/exemt/placitum-geo/internal/httpapi"
	"github.com/exemt/placitum-geo/internal/logkit"
	"github.com/exemt/placitum-geo/internal/pulse"
	"github.com/exemt/placitum-geo/internal/store"
	geopb "github.com/exemt/placitum-geo/proto"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	/*
	 * Журнал кодера -- в waf.log (internal/logkit). Шина у geo необязательна и
	 * поднимается пульсом в фоне; пока её нет, строки старта копятся и уезжают
	 * первой пачкой, а порог держит WAF_GEO_LOG до первого документа
	 * policy/log-levels.
	 */
	journal := logkit.Open(logkit.Options{Service: cfg.ServiceName, Level: cfg.LogLevel})
	defer journal.Close()

	log := journal.Log
	slog.SetDefault(log)

	data, err := store.Load(store.Paths{Country: cfg.Country, ASN: cfg.ASN}, log)
	if err != nil {
		return err
	}

	st := data.Current().Stats()
	log.Info("data loaded",
		"countries", st.Countries,
		"asns", st.ASNs,
		"skipped", st.Skipped,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()

	go data.Watch(watchCtx, cfg.ReloadEvery)
	go sighup(ctx, data, log)
	stopBeat := startHeartbeat(ctx, cfg, data, journal)
	defer stopBeat()

	errc := make(chan error, 2)

	if cfg.HTTP != "" {
		srv := &http.Server{
			Addr:              cfg.HTTP,
			Handler:           httpapi.Handler(data, log),
			ReadHeaderTimeout: 5 * time.Second,
		}

		go func() {
			log.Info("http listen", "addr", cfg.HTTP)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errc <- fmt.Errorf("http: %w", err)
			}
		}()

		go func() {
			<-ctx.Done()
			shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shut)
		}()
	}

	if cfg.GRPC != "" {
		ln, err := net.Listen("tcp", cfg.GRPC)
		if err != nil {
			return fmt.Errorf("grpc listen: %w", err)
		}

		gs := grpc.NewServer()
		geopb.RegisterGeoServer(gs, grpcapi.New(data))
		reflection.Register(gs)

		go func() {
			log.Info("grpc listen", "addr", cfg.GRPC)
			if err := gs.Serve(ln); err != nil {
				errc <- fmt.Errorf("grpc: %w", err)
			}
		}()

		go func() {
			<-ctx.Done()
			done := make(chan struct{})
			go func() {
				gs.GracefulStop()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				gs.Stop()
			}
		}()
	}

	select {
	case <-ctx.Done():
		log.Info("draining")
		return nil
	case err := <-errc:
		return err
	}
}

func startHeartbeat(ctx context.Context, cfg *config.Config, data *store.Store, journal *logkit.Journal) func() {
	log := journal.Log

	if cfg.NatsURL == "" {
		return func() {}
	}

	id := pulse.NewID()
	subject := pulse.Subject(cfg.ServiceName, id)
	done := make(chan struct{})

	go func() {
		nc, err := dialNATS(ctx, done, cfg.NatsURL, log)
		if err != nil {
			return
		}
		defer nc.Close()

		// Журнал и живой уровень -- на том же соединении, что и пульс; добить
		// накопленное -- раньше, чем оно закроется.
		journal.Attach(ctx, nc)
		defer journal.Close()

		log.Info("heartbeat on",
			"name", cfg.ServiceName,
			"id", id,
			"subject", subject,
			"every", cfg.HeartbeatEvery.String(),
		)

		beat := func() {
			st := data.Current().Stats()
			work := pulse.Work{
				Countries:   st.Countries,
				ASNs:        st.ASNs,
				Skipped:     st.Skipped,
				Gen:         st.Gen,
				Fingerprint: st.Fingerprint,
			}
			msg := pulse.Build(id, cfg.ServiceName, st.Countries > 0 || st.ASNs > 0, work)
			if err := pulse.Publish(nc, msg); err != nil {
				log.Warn("heartbeat failed", "error", err.Error())
				return
			}
			// Кадр раз в четыре секунды -- ход работы, а не событие: на info
			// он давал бы журналу контура строку каждые четыре секунды. Debug.
			log.Debug("heartbeat",
				"hostname", msg.Hostname,
				"ready", msg.Ready,
				"countries", work.Countries,
				"asns", work.ASNs,
			)
		}

		beat()
		tick := time.NewTicker(cfg.HeartbeatEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-tick.C:
				beat()
			}
		}
	}()

	return func() {
		close(done)
	}
}

func dialNATS(ctx context.Context, done <-chan struct{}, url string, log *slog.Logger) (*nats.Conn, error) {
	for {
		nc, err := nats.Connect(url,
			nats.Name("waf-geo"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(500*time.Millisecond),
		)
		if err == nil {
			return nc, nil
		}
		log.Warn("fleet bus down", "error", err.Error())
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			return nil, context.Canceled
		case <-time.After(time.Second):
		}
	}
}

func sighup(ctx context.Context, data *store.Store, log *slog.Logger) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			if _, err := data.Reload(); err != nil {
				log.Warn("sighup reload failed", "error", err.Error())
			}
		}
	}
}
