package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/service"
)

// rotasVagas registra as rotas do executor de vagas. Sem pasta, quem age é o humano.
func rotasVagas(mux *rotas, h *handler) {
	mux.HandleFunc("POST /v1/projetos/{id}/vagas", h.abrirVaga)
	mux.HandleFunc("GET /v1/projetos/{id}/vagas", h.vagas)
	mux.HandleFunc("POST /v1/projetos/{id}/vagas/{vaga}/fechar", h.fecharVaga)
	mux.HandleFunc("POST /v1/projetos/{id}/despertar", h.despertar)
}

func (h *handler) abrirVaga(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		service.VagaAbrir
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := h.s.AbrirVaga(r.Context(), r.PathValue("id"), in.Pasta, in.VagaAbrir)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vaga": v})
}

func (h *handler) fecharVaga(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta string }
	if !decode(w, r, &in) {
		return
	}
	v, err := h.s.FecharVaga(r.Context(), r.PathValue("id"), in.Pasta, r.PathValue("vaga"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vaga": v})
}

func (h *handler) vagas(w http.ResponseWriter, r *http.Request) {
	info, err := h.s.Vagas(r.Context(), r.PathValue("id"), r.URL.Query().Get("pasta"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vagas": info.Vagas, "plano_pausado_ate": info.PlanoPausadoAte,
		"plano_motivo": info.PlanoMotivo, "custo_hoje": info.CustoHoje, "execucao": info.Execucao})
}

func (h *handler) despertar(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pasta, Em, Motivo string }
	if !decode(w, r, &in) {
		return
	}
	em, err := time.ParseDuration(in.Em)
	if err != nil {
		writeServiceError(w, fmt.Errorf("%w: em %q (use uma duração como 20m ou 2h)", core.ErrInvalid, in.Em))
		return
	}
	if err := h.s.Despertar(r.Context(), r.PathValue("id"), in.Pasta, em, in.Motivo); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "em": in.Em, "motivo": in.Motivo})
}
