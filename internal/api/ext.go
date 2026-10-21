package api

import (
	"net/http"
	"strconv"
)

func rotasExtensoes(mux *rotas, h *handler) {
	mux.HandleFunc("GET /v1/projetos/{id}/extensoes", h.extensoes)
	mux.HandleFunc("POST /v1/projetos/{id}/extensoes/{nome}/{acao}", h.chamarExtensao)
	mux.HandleFunc("PUT /v1/projetos/{id}/extensoes/{nome}/entrada/{arquivo}", h.salvarEntrada)
	mux.HandleFunc("GET /v1/projetos/{id}/extensoes/{nome}/pendentes", h.pendentesExt)
	mux.HandleFunc("POST /v1/projetos/{id}/extensoes/{nome}/pendentes/{pid}", h.responderPendente)
	mux.HandleFunc("GET /v1/projetos/{id}/arquivo", h.arquivo)
}

// arquivo serve uma imagem ou vídeo de dentro da raiz do projeto (galeria).
func (h *handler) arquivo(w http.ResponseWriter, r *http.Request) {
	caminho, tipo, err := h.s.Arquivo(r.Context(), r.PathValue("id"), r.URL.Query().Get("caminho"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", tipo)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, caminho)
}

// salvarEntrada grava o corpo cru como um arquivo na pasta de entrada.
func (h *handler) salvarEntrada(w http.ResponseWriter, r *http.Request) {
	if err := h.s.SalvarEntrada(r.Context(), r.PathValue("id"), r.PathValue("nome"), r.PathValue("arquivo"), r.Body); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "arquivo": r.PathValue("arquivo")})
}

func (h *handler) pendentesExt(w http.ResponseWriter, r *http.Request) {
	st, err := h.s.Pendentes(r.Context(), r.PathValue("id"), r.PathValue("nome"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entrada": st})
}

func (h *handler) responderPendente(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.ParseInt(r.PathValue("pid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, "pendência deve ser um número")
		return
	}
	var in struct{ Campos map[string]any }
	if !decode(w, r, &in) {
		return
	}
	res, err := h.s.ResponderFormulario(r.Context(), r.PathValue("id"), r.PathValue("nome"), pid, in.Campos)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dados": res.Dados, "formulario": res.Formulario, "eventos": res.Eventos, "ms": res.MS})
}

func (h *handler) extensoes(w http.ResponseWriter, r *http.Request) {
	es, err := h.s.Extensoes(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "extensoes": es})
}

// chamarExtensao chama a ação como a vaga da pasta; sem pasta, como o HUMANO.
func (h *handler) chamarExtensao(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pasta string
		Args  map[string]any
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := h.s.Extensao(r.Context(), r.PathValue("id"), in.Pasta, r.PathValue("nome"), r.PathValue("acao"), in.Args)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dados": res.Dados, "formulario": res.Formulario, "eventos": res.Eventos, "ms": res.MS})
}
