package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/exemt/placitum-geo/internal/lookup"
	"github.com/exemt/placitum-geo/internal/store"
)

// MaxBatch — потолок одной пачки. UX режет запрос сам, это последний рубеж
// на случай чужого клиента: без него один POST мог бы гонять весь снапшот
// туда-обратно построчно.
const MaxBatch = 500

// ExpandASN -- значение `expand`, по которому системам дописывается состав.
const ExpandASN = "asn"

func Handler(st *store.Store, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /lookup", get(st, log))
	mux.HandleFunc("POST /lookup", post(st, log))
	mux.HandleFunc("POST /lookup/batch", postBatch(st, log))

	return mux
}

func get(st *store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := r.URL.Query().Get("addr")
		if addr == "" {
			addr = r.URL.Query().Get("ip")
		}

		reply(w, r, st, log, addr, optionsOf(r.URL.Query().Get("expand")))
	}
}

func post(st *store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Addr   string `json:"addr"`
			IP     string `json:"ip"`
			Expand string `json:"expand"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}

		addr := body.Addr
		if addr == "" {
			addr = body.IP
		}

		reply(w, r, st, log, addr, optionsOf(body.Expand))
	}
}

// postBatch — та же пара country+asn, но на список адресов одним походом.
// UX копит карточки в кадре и шлёт один POST вместо одного на каждую строку
// таблицы; ответ — массив в том же порядке, что и вход, битые адреса не
// валят соседей. Состава здесь нет: пачка -- для таблиц, не для бана.
func postBatch(st *store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Addrs []string `json:"addrs"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}

		if len(body.Addrs) == 0 {
			writeError(w, http.StatusBadRequest, "empty_batch")
			return
		}

		if len(body.Addrs) > MaxBatch {
			writeError(w, http.StatusBadRequest, "too_many_addrs")
			return
		}

		items := lookup.DoMany(st.Current(), body.Addrs)

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string][]lookup.BatchItem{"results": items}); err != nil {
			log.Error("write", "error", err.Error(), "path", r.URL.Path)
		}
	}
}

// Единственное расширение -- состав систем. Чужое слово -- не ошибка, а
// отсутствие просьбы: старый клиент ничего не просит и получает прежнее.
func optionsOf(expand string) lookup.Options {
	return lookup.Options{ExpandASN: expand == ExpandASN}
}

func reply(w http.ResponseWriter, r *http.Request, st *store.Store, log *slog.Logger, addr string, opt lookup.Options) {
	got, err := lookup.DoWith(st.Current(), addr, opt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(got); err != nil {
		log.Error("write", "error", err.Error(), "path", r.URL.Path)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
