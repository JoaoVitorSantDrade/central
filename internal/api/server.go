// Package api expõe o serviço pelo contrato HTTP /v1. Não tem lógica própria.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/service"
	"github.com/JoaoVitorSantDrade/central/internal/version"
)

// Limites do long-poll de esperar.
const (
	EsperarPadrao = 30 * time.Minute
	EsperarMaximo = 60 * time.Minute
)

type handler struct{ s *service.Service }

// rotas registra as rotas no mux e guarda a lista ("MÉTODO /caminho") para o
// golden do contrato /v1.
type rotas struct {
	mux   *http.ServeMux
	lista []string
}

func (r *rotas) HandleFunc(padrao string, fn func(http.ResponseWriter, *http.Request)) {
	r.mux.HandleFunc(padrao, fn)
	r.lista = append(r.lista, padrao)
}

// Rotas lista as rotas do contrato /v1 ("MÉTODO /caminho").
func Rotas() []string { return montar(&handler{}).lista }

// NewHandler monta as rotas /v1.
func NewHandler(s *service.Service) http.Handler { return montar(&handler{s: s}).mux }

func montar(h *handler) *rotas {
	mux := &rotas{mux: http.NewServeMux()}
	mux.HandleFunc("GET /v1/saude", h.saude)
	mux.HandleFunc("POST /v1/projetos/{id}/agentes", h.entrar)
	mux.HandleFunc("GET /v1/projetos/{id}/agentes", h.agentes)
	mux.HandleFunc("POST /v1/projetos/{id}/status", h.status)
	mux.HandleFunc("POST /v1/projetos/{id}/sair", h.sair)
	mux.HandleFunc("POST /v1/projetos/{id}/mensagens", h.enviar)
	mux.HandleFunc("POST /v1/projetos/{id}/responder", h.responder)
	mux.HandleFunc("GET /v1/projetos/{id}/pendentes", h.pendentes)
	mux.HandleFunc("POST /v1/projetos/{id}/caixa", h.caixa)
	mux.HandleFunc("GET /v1/projetos/{id}/esperar", h.esperar)
	mux.HandleFunc("POST /v1/projetos/{id}/travas", h.travar)
	mux.HandleFunc("DELETE /v1/projetos/{id}/travas/{nome...}", h.soltar)
	mux.HandleFunc("GET /v1/projetos/{id}/travas", h.travas)
	mux.HandleFunc("POST /v1/projetos/{id}/recursos/{nome}/pedidos", h.pedir)
	mux.HandleFunc("GET /v1/projetos/{id}/recursos/{nome}/pedidos/{gid}/esperar", h.esperarConcessao)
	mux.HandleFunc("DELETE /v1/projetos/{id}/recursos/{nome}/concessao", h.soltarConcessao)
	mux.HandleFunc("DELETE /v1/projetos/{id}/recursos/{nome}/pedidos/{gid}", h.soltarPedido)
	mux.HandleFunc("GET /v1/projetos/{id}/recursos/{nome}", h.recurso)
	mux.HandleFunc("POST /v1/projetos/{id}/fatos/{chave}", h.gravarFato)
	mux.HandleFunc("GET /v1/projetos/{id}/fatos/{chave}", h.fato)
	mux.HandleFunc("GET /v1/projetos/{id}/fatos/{chave}/historico", h.fatoHistorico)
	mux.HandleFunc("GET /v1/projetos/{id}/fatos", h.fatos)
	mux.HandleFunc("POST /v1/projetos/{id}/ids/{prefixo}", h.gerarID)
	mux.HandleFunc("POST /v1/projetos/{id}/execucoes", h.iniciarExecucao)
	mux.HandleFunc("POST /v1/projetos/{id}/execucoes/{eid}/batimento", h.baterExecucao)
	mux.HandleFunc("POST /v1/projetos/{id}/execucoes/{eid}/fim", h.encerrarExecucao)
	rotasProcesso(mux, h)
	rotasPainel(mux, h)
	rotasExtensoes(mux, h)
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

func (h *handler) saude(w http.ResponseWriter, r *http.Request) {
	// dados e pid deixam a CLI conferir que fala com o daemon da sua pasta de dados.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "versao": version.Versao, "api": version.API,
		"dados": h.s.DataDir(), "pid": os.Getpid()})
}

func (h *handler) entrar(w http.ResponseWriter, r *http.Request) {
	var in service.EnterInput
	if !decode(w, r, &in) {
		return
	}
	in.ProjetoID = r.PathValue("id")
	a, err := h.s.Enter(r.Context(), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agente": a})
}

func (h *handler) agentes(w http.ResponseWriter, r *http.Request) {
	as, err := h.s.Agents(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agentes": as})
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Estado, Status string }
	if !decode(w, r, &in) {
		return
	}
	a, err := h.s.Status(r.Context(), r.PathValue("id"), in.Pasta, in.Estado, in.Status)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agente": a})
}

func (h *handler) sair(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta string }
	if !decode(w, r, &in) {
		return
	}
	a, err := h.s.Leave(r.Context(), r.PathValue("id"), in.Pasta)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "agente": a})
}

func (h *handler) enviar(w http.ResponseWriter, r *http.Request) {
	// Responder é o prazo de resposta como duração (ex.: 30m); vazio, sem prazo.
	var in struct{ Pasta, Para, Tipo, Ref, Corpo, Chave, Responder string }
	if !decode(w, r, &in) {
		return
	}
	var prazo time.Duration
	if in.Responder != "" {
		d, err := time.ParseDuration(in.Responder)
		if err != nil || d <= 0 {
			writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "responder deve ser uma duração positiva (ex.: 30m)")
			return
		}
		prazo = d
	}
	m, err := h.s.Send(r.Context(), r.PathValue("id"), in.Pasta, core.SendInput{
		Para: in.Para, Tipo: in.Tipo, Ref: in.Ref, Corpo: in.Corpo, Chave: in.Chave, ResponderEm: prazo})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mensagem": m})
}

func (h *handler) responder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		Seq   int64
		Corpo string
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := h.s.Reply(r.Context(), r.PathValue("id"), in.Pasta, in.Seq, in.Corpo)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mensagem": m})
}

func (h *handler) pendentes(w http.ResponseWriter, r *http.Request) {
	p, err := h.s.Pending(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
		core.Pendencias
	}{true, p})
}

func (h *handler) caixa(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta string }
	if !decode(w, r, &in) {
		return
	}
	ms, err := h.s.Inbox(r.Context(), r.PathValue("id"), in.Pasta)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if ms == nil {
		ms = []core.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mensagens": ms})
}

func (h *handler) travar(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Nome, Motivo string }
	if !decode(w, r, &in) {
		return
	}
	l, err := h.s.Lock(r.Context(), r.PathValue("id"), in.Pasta, in.Nome, in.Motivo)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trava": l})
}

func (h *handler) soltar(w http.ResponseWriter, r *http.Request) {
	nome := r.PathValue("nome")
	if err := h.s.Unlock(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), nome); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "solta": nome})
}

func (h *handler) travas(w http.ResponseWriter, r *http.Request) {
	ls, err := h.s.Locks(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "travas": ls})
}

// ateParam lê o prazo do long-poll (?ate=); fora de (0, EsperarMaximo] escreve o erro.
func ateParam(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	s := r.URL.Query().Get("ate")
	if s == "" {
		return EsperarPadrao, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > EsperarMaximo {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido,
			fmt.Sprintf("ate deve ser uma duração entre 0 e %v (ex.: 30m)", EsperarMaximo))
		return 0, false
	}
	return d, true
}

// writeWaitError escreve o erro de um long-poll: com o contexto da requisição
// cancelado, o daemon está encerrando (ou o cliente desconectou, e então
// ninguém lê isto).
func writeWaitError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		writeError(w, http.StatusServiceUnavailable, CodigoReiniciando, "o daemon está reiniciando; tente de novo")
		return
	}
	writeServiceError(w, err)
}

func (h *handler) esperar(w http.ResponseWriter, r *http.Request) {
	ate, ok := ateParam(w, r)
	if !ok {
		return
	}
	res, err := h.s.Wait(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), ate)
	if err != nil {
		writeWaitError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
		service.WaitResult
	}{true, res})
}

func (h *handler) pedir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta, Tipo, Prioridade, Motivo, Chave string
		Dims                                   core.Dims
		Exclusivo                              bool
		Min                                    int64
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Min < 0 {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "min deve ser um número de minutos positivo")
		return
	}
	g, err := h.s.RequestGrant(r.Context(), r.PathValue("id"), in.Pasta, core.GrantRequest{
		Recurso: r.PathValue("nome"), Tipo: in.Tipo, Prioridade: in.Prioridade, Motivo: in.Motivo, Chave: in.Chave,
		Declarado: in.Dims, Exclusivo: in.Exclusivo, Duracao: time.Duration(in.Min) * time.Minute})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "concessao": g})
}

func (h *handler) esperarConcessao(w http.ResponseWriter, r *http.Request) {
	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "id de pedido inválido")
		return
	}
	ate, ok := ateParam(w, r)
	if !ok {
		return
	}
	res, err := h.s.WaitGrant(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("nome"), gid, ate)
	if err != nil {
		writeWaitError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
		service.GrantWaitResult
	}{true, res})
}

func (h *handler) soltarConcessao(w http.ResponseWriter, r *http.Request) {
	g, err := h.s.ReleaseGrant(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("nome"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "concessao": g})
}

func (h *handler) soltarPedido(w http.ResponseWriter, r *http.Request) {
	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "id de pedido inválido")
		return
	}
	g, err := h.s.ReleaseGrantByID(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("nome"), gid)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "concessao": g})
}

func (h *handler) recurso(w http.ResponseWriter, r *http.Request) {
	v, err := h.s.ResourceStatus(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("nome"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "recurso": v})
}

func (h *handler) iniciarExecucao(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta, Rotulo, Comando string
		Concessao              int64
	}
	if !decode(w, r, &in) {
		return
	}
	e, err := h.s.StartExecution(r.Context(), r.PathValue("id"), in.Pasta, in.Rotulo, in.Comando, in.Concessao)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "execucao": e})
}

// eidParam lê o id da execução do caminho; inválido, escreve o erro.
func eidParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	eid, err := strconv.ParseInt(r.PathValue("eid"), 10, 64)
	if err != nil || eid <= 0 {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "id de execução inválido")
		return 0, false
	}
	return eid, true
}

func (h *handler) baterExecucao(w http.ResponseWriter, r *http.Request) {
	eid, ok := eidParam(w, r)
	if !ok {
		return
	}
	var in struct{ Pasta string }
	if !decode(w, r, &in) {
		return
	}
	if err := h.s.BeatExecution(r.Context(), r.PathValue("id"), in.Pasta, eid); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) encerrarExecucao(w http.ResponseWriter, r *http.Request) {
	eid, ok := eidParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Pasta    string
		Codigo   int
		FinalLog string `json:"final_log"`
	}
	if !decode(w, r, &in) {
		return
	}
	e, err := h.s.FinishExecution(r.Context(), r.PathValue("id"), in.Pasta, eid, in.Codigo, in.FinalLog)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "execucao": e})
}

func (h *handler) gravarFato(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta, Motivo string
		Valor         json.RawMessage
	}
	if !decode(w, r, &in) {
		return
	}
	f, err := h.s.SetFact(r.Context(), r.PathValue("id"), in.Pasta, r.PathValue("chave"), in.Valor, in.Motivo)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fato": f})
}

func (h *handler) fato(w http.ResponseWriter, r *http.Request) {
	f, err := h.s.GetFact(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("chave"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fato": f})
}

func (h *handler) fatoHistorico(w http.ResponseWriter, r *http.Request) {
	fs, err := h.s.FactHistory(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"), r.PathValue("chave"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fatos": fs})
}

func (h *handler) fatos(w http.ResponseWriter, r *http.Request) {
	fs, err := h.s.Facts(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fatos": fs})
}

func (h *handler) gerarID(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta  string
		Minimo int
	}
	if !decode(w, r, &in) {
		return
	}
	id, err := h.s.NextID(r.Context(), r.PathValue("id"), in.Pasta, r.PathValue("prefixo"), in.Minimo)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}
