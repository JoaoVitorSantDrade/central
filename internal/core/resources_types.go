package core

import (
	"encoding/json"
	"time"
)

// Dims são valores por dimensão de um recurso (o núcleo não sabe o que cada uma mede).
type Dims map[string]float64

// TipoSpec é o valor de partida de um tipo de pedido: as dimensões ou exclusivo.
type TipoSpec struct {
	Dims      Dims `json:"dims,omitempty"`
	Exclusivo bool `json:"exclusivo"`
}

// SemUsoSpec avisa quando a leitura menos o reservado fica abaixo de Limite por Apos.
// Apos vai no JSON como apos_ms.
type SemUsoSpec struct {
	Dimensao string        `json:"dimensao"`
	Limite   float64       `json:"limite"`
	Apos     time.Duration `json:"-"`
}

type semUsoFields SemUsoSpec

type semUsoJSON struct {
	semUsoFields
	AposMs int64 `json:"apos_ms"`
}

// MarshalJSON escreve Apos como apos_ms.
func (s SemUsoSpec) MarshalJSON() ([]byte, error) {
	return json.Marshal(semUsoJSON{semUsoFields(s), s.Apos.Milliseconds()})
}

// UnmarshalJSON lê apos_ms de volta em Apos.
func (s *SemUsoSpec) UnmarshalJSON(b []byte) error {
	var v semUsoJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = SemUsoSpec(v.semUsoFields)
	s.Apos = time.Duration(v.AposMs) * time.Millisecond
	return nil
}

// ResourceSpec é um recurso com dimensões já validado (spec §5.3).
// MedidorIntervalo zero quer dizer recurso sem medidor. As durações vão no
// JSON em milissegundos (prazo_max_ms, prazo_max_por_prioridade_ms,
// medidor_intervalo_ms).
type ResourceSpec struct {
	Nome                  string                   `json:"nome"`
	Dimensoes             Dims                     `json:"dimensoes"`
	Reservado             Dims                     `json:"reservado"`
	Margem                Dims                     `json:"margem"`
	TetoMedido            Dims                     `json:"teto_medido"` // fração de Dimensoes
	Prioridades           []string                 `json:"prioridades"`
	PrazoMax              time.Duration            `json:"-"`
	PrazoMaxPorPrioridade map[string]time.Duration `json:"-"`
	Tipos                 map[string]TipoSpec      `json:"tipos"`
	SemUso                *SemUsoSpec              `json:"sem_uso"`
	MedidorIntervalo      time.Duration            `json:"-"`
}

type resourceSpecFields ResourceSpec

type resourceSpecJSON struct {
	resourceSpecFields
	PrazoMaxMs              int64            `json:"prazo_max_ms"`
	PrazoMaxPorPrioridadeMs map[string]int64 `json:"prazo_max_por_prioridade_ms"`
	MedidorIntervaloMs      int64            `json:"medidor_intervalo_ms"`
}

// MarshalJSON escreve as durações em milissegundos.
func (r ResourceSpec) MarshalJSON() ([]byte, error) {
	v := resourceSpecJSON{resourceSpecFields: resourceSpecFields(r),
		PrazoMaxMs: r.PrazoMax.Milliseconds(), MedidorIntervaloMs: r.MedidorIntervalo.Milliseconds()}
	if r.PrazoMaxPorPrioridade != nil {
		v.PrazoMaxPorPrioridadeMs = map[string]int64{}
		for p, d := range r.PrazoMaxPorPrioridade {
			v.PrazoMaxPorPrioridadeMs[p] = d.Milliseconds()
		}
	}
	return json.Marshal(v)
}

// UnmarshalJSON lê as durações em milissegundos de volta.
func (r *ResourceSpec) UnmarshalJSON(b []byte) error {
	var v resourceSpecJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*r = ResourceSpec(v.resourceSpecFields)
	r.PrazoMax = time.Duration(v.PrazoMaxMs) * time.Millisecond
	r.MedidorIntervalo = time.Duration(v.MedidorIntervaloMs) * time.Millisecond
	if v.PrazoMaxPorPrioridadeMs != nil {
		r.PrazoMaxPorPrioridade = map[string]time.Duration{}
		for p, ms := range v.PrazoMaxPorPrioridadeMs {
			r.PrazoMaxPorPrioridade[p] = time.Duration(ms) * time.Millisecond
		}
	}
	return nil
}

// Estados de uma concessão.
const (
	GrantFila      = "fila"
	GrantAtiva     = "ativa"
	GrantEncerrada = "encerrada"
)

// De onde veio o declarado de uma concessão.
const (
	EstimativaDeclarada = "declarada"
	EstimativaHistorico = "historico"
	EstimativaTipo      = "tipo"
	EstimativaExclusivo = "exclusivo"
)

// Grant é uma concessão de recurso: na fila, ativa ou encerrada.
// Duracao vai no JSON como duracao_ms (inteiro, milissegundos).
type Grant struct {
	ID         int64         `json:"id"`
	Recurso    string        `json:"recurso"`
	Vaga       string        `json:"vaga"`
	Tipo       string        `json:"tipo"`
	Prioridade string        `json:"prioridade"`
	Motivo     string        `json:"motivo"`
	Estado     string        `json:"estado"`
	FimMotivo  string        `json:"fim_motivo,omitempty"`
	Estimativa string        `json:"estimativa"`
	Declarado  Dims          `json:"declarado"`
	Pico       Dims          `json:"pico,omitempty"`
	Exclusivo  bool          `json:"exclusivo"`
	Sozinha    bool          `json:"sozinha"`
	Duracao    time.Duration `json:"-"`
	PedidoEm   time.Time     `json:"pedido_em"`
	Inicio     *time.Time    `json:"inicio,omitempty"`
	Prazo      *time.Time    `json:"prazo,omitempty"`
	Fim        *time.Time    `json:"fim,omitempty"`
}

// grantJSON é o Grant com a duração em milissegundos.
type grantJSON struct {
	grantFields
	DuracaoMs int64 `json:"duracao_ms"`
}

// grantFields tem os campos do Grant sem os métodos de JSON (evita recursão).
type grantFields Grant

// MarshalJSON escreve Duracao como duracao_ms.
func (g Grant) MarshalJSON() ([]byte, error) {
	return json.Marshal(grantJSON{grantFields(g), g.Duracao.Milliseconds()})
}

// UnmarshalJSON lê duracao_ms de volta em Duracao.
func (g *Grant) UnmarshalJSON(b []byte) error {
	var v grantJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*g = Grant(v.grantFields)
	g.Duracao = time.Duration(v.DuracaoMs) * time.Millisecond
	return nil
}

// Sample é uma leitura do medidor de um recurso.
type Sample struct {
	Quando  time.Time `json:"quando"`
	Valores Dims      `json:"valores"`
}
