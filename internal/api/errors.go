package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/JoaoVitorSantDrade/central/internal/core"
	"github.com/JoaoVitorSantDrade/central/internal/service"
)

// Códigos de erro públicos do contrato /v1.
const (
	CodigoNaoEncontrado = "nao_encontrado"
	CodigoInvalido      = "invalido"
	CodigoConflito      = "conflito"
	CodigoRecusado      = "recusado"
	CodigoInterno       = "interno"
	CodigoReiniciando   = "daemon_reiniciando"
	CodigoExtensao      = "extensao"
)

type errorBody struct {
	OK   bool `json:"ok"`
	Erro struct {
		Codigo   string `json:"codigo"`
		Mensagem string `json:"mensagem"`
	} `json:"erro"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, codigo, mensagem string) {
	var b errorBody
	b.Erro.Codigo, b.Erro.Mensagem = codigo, mensagem
	writeJSON(w, status, b)
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, CodigoNaoEncontrado, err.Error())
	case errors.Is(err, core.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, CodigoInvalido, err.Error())
	case errors.Is(err, core.ErrConflict):
		writeError(w, http.StatusConflict, CodigoConflito, err.Error())
	case errors.Is(err, service.ErrExtensao):
		writeError(w, http.StatusBadGateway, CodigoExtensao, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, CodigoInterno, err.Error())
	}
}
