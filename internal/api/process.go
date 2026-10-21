package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/process"
	"github.com/JoaoVitorSantDrade/central/internal/service"
)

// rotasProcesso registra as rotas do processo (spec §6).
func rotasProcesso(mux *rotas, h *handler) {
	mux.HandleFunc("POST /v1/projetos/{id}/sprint", h.abrirSprint)
	mux.HandleFunc("GET /v1/projetos/{id}/sprint", h.verSprint)
	mux.HandleFunc("POST /v1/projetos/{id}/sprint/antecipar", h.anteciparSprint)
	mux.HandleFunc("POST /v1/projetos/{id}/sprint/encerrar", h.encerrarSprint)
	rotasItens(mux, h)
	mux.HandleFunc("GET /v1/projetos/{id}/proxima", h.proxima)
	mux.HandleFunc("POST /v1/projetos/{id}/decisoes", h.abrirDecisao)
	mux.HandleFunc("GET /v1/projetos/{id}/saude", h.saudeProjeto)
	mux.HandleFunc("GET /v1/projetos/{id}/orfas", h.orfas)
	mux.HandleFunc("POST /v1/projetos/{id}/orfas/liberar", h.orfas)
	mux.HandleFunc("GET /v1/projetos/{id}/metricas", h.metricas)
	mux.HandleFunc("POST /v1/projetos/{id}/importar-locks", h.locks)
	mux.HandleFunc("GET /v1/projetos/{id}/regras", h.regras)
	mux.HandleFunc("GET /v1/projetos/{id}/brief", h.brief)
	mux.HandleFunc("POST /v1/projetos/{id}/regras", h.salvarRegra)
	mux.HandleFunc("POST /v1/projetos/{id}/regras/{rid}/estado", h.estadoRegra)
	mux.HandleFunc("POST /v1/projetos/{id}/regras/importar", h.importarLicoes)
	mux.HandleFunc("POST /v1/projetos/{id}/exportar-locks", h.locks)
	mux.HandleFunc("POST /v1/projetos/{id}/decisoes/{did}/{op}", h.decisao)
}

func (h *handler) abrirSprint(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Expediente, ID string }
	if !decode(w, r, &in) {
		return
	}
	s, err := h.s.AbrirSprint(r.Context(), r.PathValue("id"), in.Pasta, in.Expediente, in.ID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sprint": s})
}

func (h *handler) verSprint(w http.ResponseWriter, r *http.Request) {
	s, err := h.s.VerSprint(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sprint": s})
}

// sprintAcao é o formato comum de antecipar e encerrar: só a pasta.
func (h *handler) sprintAcao(w http.ResponseWriter, r *http.Request, f func(s *service.Service, id, pasta string) (any, error)) {
	var in struct{ Pasta string }
	if !decode(w, r, &in) {
		return
	}
	v, err := f(h.s, r.PathValue("id"), in.Pasta)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sprint": v})
}

func (h *handler) anteciparSprint(w http.ResponseWriter, r *http.Request) {
	h.sprintAcao(w, r, func(s *service.Service, id, pasta string) (any, error) {
		return s.AnteciparSprint(r.Context(), id, pasta)
	})
}

func (h *handler) encerrarSprint(w http.ResponseWriter, r *http.Request) {
	h.sprintAcao(w, r, func(s *service.Service, id, pasta string) (any, error) {
		return s.EncerrarSprint(r.Context(), id, pasta)
	})
}

func rotasItens(mux *rotas, h *handler) {
	mux.HandleFunc("POST /v1/projetos/{id}/itens", h.criarItem)
	mux.HandleFunc("GET /v1/projetos/{id}/itens", h.itens)
	mux.HandleFunc("GET /v1/projetos/{id}/itens/{iid}", h.item)
	mux.HandleFunc("POST /v1/projetos/{id}/itens/{iid}/{acao}", h.acaoItem)
}

func (h *handler) criarItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		process.ItemInput
	}
	if !decode(w, r, &in) {
		return
	}
	it, err := h.s.CriarItem(r.Context(), r.PathValue("id"), in.Pasta, in.ItemInput)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": it})
}

func (h *handler) itens(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lista := func(k string) []string {
		if v := q.Get(k); v != "" {
			return strings.Split(v, ",")
		}
		return nil
	}
	f := core.ItemFilter{Estados: lista("estado"), Tipos: lista("tipo"), IDs: lista("ids"), Dono: q.Get("dono"), Sprint: q.Get("sprint")}
	its, err := h.s.Itens(r.Context(), r.PathValue("id"), q.Get("pasta"), f)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "itens": its})
}

func (h *handler) item(w http.ResponseWriter, r *http.Request) {
	it, notas, err := h.s.VerItem(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("iid"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": it, "notas": notas})
}

func (h *handler) acaoItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		process.Args
	}
	if !decode(w, r, &in) {
		return
	}
	it, err := h.s.AcaoItem(r.Context(), r.PathValue("id"), in.Pasta, r.PathValue("acao"), r.PathValue("iid"), in.Args)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": it})
}

func (h *handler) proxima(w http.ResponseWriter, r *http.Request) {
	esc, err := h.s.Proxima(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": esc.Item, "motivo": esc.Motivo, "descartados": esc.Descartados,
		"sugestao": sugestaoProxima(esc)})
}

func sugestaoProxima(esc process.Escolha) string {
	if esc.Item == nil {
		return ""
	}
	return "central reservar " + esc.Item.ID
}

func (h *handler) decisao(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		service.DecisaoInput
	}
	if !decode(w, r, &in) {
		return
	}
	op, id := r.PathValue("op"), r.PathValue("did")
	var it any
	var err error
	if op == "veto" || op == "responder" {
		it, err = h.s.DecisaoHumano(r.Context(), r.PathValue("id"), op, id, in.DecisaoInput)
	} else {
		it, err = h.s.Decisao(r.Context(), r.PathValue("id"), in.Pasta, op, id, in.DecisaoInput)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "decisao": it})
}

func (h *handler) abrirDecisao(w http.ResponseWriter, r *http.Request) {
	r.SetPathValue("op", "abrir")
	h.decisao(w, r)
}

func (h *handler) saudeProjeto(w http.ResponseWriter, r *http.Request) {
	s, err := h.s.Saude(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saude": s})
}

func (h *handler) orfas(w http.ResponseWriter, r *http.Request) {
	liberar := r.Method == http.MethodPost
	pasta := r.URL.Query().Get("pasta")
	if liberar {
		var in struct{ Pasta string }
		if !decode(w, r, &in) {
			return
		}
		pasta = in.Pasta
	}
	its, err := h.s.Orfas(r.Context(), r.PathValue("id"), pasta, liberar)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "itens": its})
}

func (h *handler) metricas(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m, err := h.s.Metricas(r.Context(), r.PathValue("id"), q.Get("pasta"), q.Get("sprint"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "metricas": m, "resumo": m.Texto()})
}

func (h *handler) locks(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Dir string }
	if !decode(w, r, &in) {
		return
	}
	var v any
	var err error
	if strings.HasSuffix(r.URL.Path, "/importar-locks") {
		v, err = h.s.ImportLocks(r.Context(), r.PathValue("id"), in.Pasta, in.Dir)
	} else {
		v, err = h.s.ExportLocks(r.Context(), r.PathValue("id"), in.Pasta, in.Dir)
	}
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resumo": v})
}

func (h *handler) regras(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rs, err := h.s.Regras(r.Context(), r.PathValue("id"), q.Get("pasta"), core.RuleFilter{Papel: q.Get("papel"), Estado: q.Get("estado")})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "regras": rs})
}

func (h *handler) salvarRegra(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		core.Rule
	}
	if !decode(w, r, &in) {
		return
	}
	rr, err := h.s.SalvarRegra(r.Context(), r.PathValue("id"), in.Pasta, in.Rule)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "regra": rr})
}

func (h *handler) estadoRegra(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Estado, Comando string }
	if !decode(w, r, &in) {
		return
	}
	rr, err := h.s.EstadoRegra(r.Context(), r.PathValue("id"), in.Pasta, r.PathValue("rid"), in.Estado, in.Comando)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "regra": rr})
}

func (h *handler) importarLicoes(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Arquivo string }
	if !decode(w, r, &in) {
		return
	}
	res, err := h.s.ImportarLicoes(r.Context(), r.PathValue("id"), in.Pasta, in.Arquivo)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resumo": res})
}

func (h *handler) brief(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	max := 0
	if v := q.Get("max_tokens"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 100 {
			writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "max_tokens precisa ser um número de pelo menos 100")
			return
		}
		max = n
	}
	b, err := h.s.Brief(r.Context(), r.PathValue("id"), q.Get("pasta"), q.Get("curto") == "1", max)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "brief": b})
}
