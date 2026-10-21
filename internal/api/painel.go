package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
)

// Parâmetros de leitura do painel.
const (
	LimiteMensagensPadrao = 200
	// JanelaAmostrasPadrao é o período das amostras quando de/ate não vêm.
	JanelaAmostrasPadrao = 6 * time.Hour
	// VidaStream é de quanto em quanto tempo o stream manda um comentário de vida.
	VidaStream = 15 * time.Second
	// ReleituraStream é o intervalo em que o stream relê os eventos mesmo sem
	// notificação (nem toda gravação notifica).
	ReleituraStream = time.Second
	// LoteStream é o máximo de eventos lidos de uma vez pelo stream.
	LoteStream = 500
)

func rotasPainel(mux *rotas, h *handler) {
	mux.HandleFunc("GET /v1/projetos", h.projetos)
	mux.HandleFunc("GET /v1/projetos/{id}/mensagens", h.mensagens)
	mux.HandleFunc("GET /v1/projetos/{id}/eventos", h.eventos)
	mux.HandleFunc("GET /v1/projetos/{id}/recursos", h.recursos)
	mux.HandleFunc("GET /v1/projetos/{id}/recursos/{nome}/amostras", h.amostras)
	mux.HandleFunc("GET /v1/projetos/{id}/stream", h.stream)
	mux.HandleFunc("POST /v1/projetos/{id}/humano/mensagens", h.humanoEnviar)
}

// queryInt lê um inteiro não negativo da query; vazio devolve padrao.
func queryInt(w http.ResponseWriter, r *http.Request, nome string, padrao int64) (int64, bool) {
	s := r.URL.Query().Get(nome)
	if s == "" {
		return padrao, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, nome+" deve ser um inteiro não negativo")
		return 0, false
	}
	return n, true
}

// queryTime lê um horário RFC 3339 da query; vazio devolve padrao.
func queryTime(w http.ResponseWriter, r *http.Request, nome string, padrao time.Time) (time.Time, bool) {
	s := r.URL.Query().Get(nome)
	if s == "" {
		return padrao, true
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, nome+" deve ser um horário RFC 3339 (ex.: 2026-10-21T09:00:00Z)")
		return time.Time{}, false
	}
	return t, true
}

// lista separa uma lista por vírgulas, sem itens vazios.
func lista(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (h *handler) projetos(w http.ResponseWriter, r *http.Request) {
	ps, err := h.s.Projetos(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "projetos": ps})
}

func (h *handler) mensagens(w http.ResponseWriter, r *http.Request) {
	depois, ok := queryInt(w, r, "depois", 0)
	if !ok {
		return
	}
	limite, ok := queryInt(w, r, "limite", LimiteMensagensPadrao)
	if !ok {
		return
	}
	hist, err := h.s.Mensagens(r.Context(), r.PathValue("id"), depois, int(limite))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mensagens": hist.Mensagens, "pendencias": hist.Pendencias})
}

func (h *handler) eventos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := core.EventFilter{Tipos: lista(q.Get("tipos")), Prefixos: lista(q.Get("prefixos")), Ref: q.Get("ref")}
	var ok bool
	if f.DepoisDe, ok = queryInt(w, r, "depois", 0); !ok {
		return
	}
	limite, ok := queryInt(w, r, "limite", 0)
	if !ok {
		return
	}
	f.Limite = int(limite)
	if f.De, ok = queryTime(w, r, "de", time.Time{}); !ok {
		return
	}
	if f.Ate, ok = queryTime(w, r, "ate", time.Time{}); !ok {
		return
	}
	evs, ultimo, err := h.s.Eventos(r.Context(), r.PathValue("id"), f)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "eventos": evs, "ultimo": ultimo})
}

func (h *handler) recursos(w http.ResponseWriter, r *http.Request) {
	rs, err := h.s.Recursos(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recursos": rs})
}

func (h *handler) amostras(w http.ResponseWriter, r *http.Request) {
	agora := time.Now()
	ate, ok := queryTime(w, r, "ate", agora)
	if !ok {
		return
	}
	de, ok := queryTime(w, r, "de", ate.Add(-JanelaAmostrasPadrao))
	if !ok {
		return
	}
	sr, err := h.s.Amostras(r.Context(), r.PathValue("id"), r.PathValue("nome"), de, ate)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "amostras": sr.Amostras, "concessoes": sr.Concessoes})
}

// stream manda os eventos novos do projeto como Server-Sent Events, cada um
// com id = seq (o EventSource retoma pelo Last-Event-ID).
func (h *handler) stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	depois, ok := queryInt(w, r, "depois", 0)
	if !ok {
		return
	}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if n, err := strconv.ParseInt(last, 10, 64); err == nil && n > depois {
			depois = n
		}
	}
	ctx := r.Context()
	if _, err := h.s.Mudancas(ctx, id); err != nil {
		writeServiceError(w, err)
		return
	}
	flusher, _ := w.(http.Flusher)
	// O stream fica aberto: tira o prazo de escrita do servidor, se houver.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": central\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	releitura := time.NewTicker(ReleituraStream)
	defer releitura.Stop()
	vida := time.NewTicker(VidaStream)
	defer vida.Stop()
	for {
		// Pega o canal antes de ler, para não perder uma mudança entre a leitura e a espera.
		mudou, err := h.s.Mudancas(ctx, id)
		if err != nil {
			return
		}
		evs, _, err := h.s.Eventos(ctx, id, core.EventFilter{DepoisDe: depois, Limite: LoteStream})
		if err != nil {
			return
		}
		for _, ev := range evs {
			b, err := json.Marshal(ev)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, b); err != nil {
				return
			}
			depois = ev.Seq
		}
		if len(evs) > 0 && flusher != nil {
			flusher.Flush()
		}
		if len(evs) == LoteStream {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-mudou:
		case <-releitura.C:
		case <-vida.C:
			if _, err := fmt.Fprint(w, ": vida\n\n"); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (h *handler) humanoEnviar(w http.ResponseWriter, r *http.Request) {
	var in struct{ Para, Tipo, Ref, Corpo string }
	if !decode(w, r, &in) {
		return
	}
	m, err := h.s.HumanoEnviar(r.Context(), r.PathValue("id"), core.SendInput{Para: in.Para, Tipo: in.Tipo, Ref: in.Ref, Corpo: in.Corpo})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mensagem": m})
}
